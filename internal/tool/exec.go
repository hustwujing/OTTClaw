// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/tool/exec.go — exec / exec_run 工具实现（PTY 版）
//
// 执行流程（两步 + 可选轮询）：
//
//  1. exec(command, ...)
//       创建 pending 记录 → 通过 InteractiveSender 向前端推送确认框
//       → 返回 {status:"pending_approval", pending_id:"..."}
//       LLM 收到后必须停止，等待用户在下一轮点击确认/取消。
//
//  2. 用户点击「确认执行」→ 文本作为下一条用户消息发回
//     LLM 调用 exec_run(pending_id) 真正执行命令
//       → 命令在 yield_ms 内完成：同步返回完整输出
//       → 超过 yield_ms 仍在运行：后台化，返回 session_id
//
//  3. 用 process(action="poll", session_id=...) 轮询后台命令输出和状态
//
// PTY 设计：
//   - pty.StartWithSize 为每个命令分配一个伪终端，bash 认为自己在真实终端里。
//   - 进度条、颜色、交互程序均可正常运行。
//   - 双缓冲：aggBuf 保存完整历史（供 process log），drainBuf 保存增量输出（供 process poll）。
//   - readDone channel：read goroutine 读完 ptmx 后关闭，确保 doneCh 关闭前所有输出已刷入缓冲。
//
// 安全设计：
//   - 审批为系统层强制：exec 不直接执行，必须经过用户确认后调 exec_run
//   - pending 5 分钟过期，过期后 exec_run 拒绝执行
//   - 超时通过 context.WithTimeout + cmd.Cancel(killGroup) 实现，killGroup 向进程组发 SIGKILL
//   - 输出上限 200 KB，超出自动截断（aggBuf 层截断）
package tool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"OTTClaw/config"
)

// ── 常量 ──────────────────────────────────────────────────────────────────────

const (
	execMaxOutputBytes = 200 * 1024      // 输出上限 200 KB
	execPendingTTL     = 5 * time.Minute // pending 审批过期时间
	execSessionTTL     = 2 * time.Hour   // 已完成 session 保留 2 小时
)

// ── 并发安全输出缓冲（全量历史） ───────────────────────────────────────────────

type execBuf struct {
	mu     sync.Mutex
	b      bytes.Buffer
	capped bool
}

func (b *execBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capped {
		return len(p), nil
	}
	if b.b.Len()+len(p) > execMaxOutputBytes {
		rem := execMaxOutputBytes - b.b.Len()
		if rem > 0 {
			b.b.Write(p[:rem])
		}
		b.b.WriteString("\n… [output truncated at 200 KB]")
		b.capped = true
		return len(p), nil
	}
	return b.b.Write(p)
}

func (b *execBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// ── pending 审批记录 ──────────────────────────────────────────────────────────

type execPending struct {
	id         string
	command    string
	workdir    string
	env        map[string]string
	timeoutSec int
	yieldMs    int
	background bool
	createdAt  time.Time
}

var pendingStore = struct {
	mu   sync.Mutex
	data map[string]*execPending
}{data: make(map[string]*execPending)}

// ── execSession（运行中 / 已完成）────────────────────────────────────────────

type execSession struct {
	id        string
	command   string
	startedAt time.Time
	doneCh    chan struct{}
	readDone  chan struct{} // 关闭时表示 pty read goroutine 已退出，所有输出已落入缓冲

	ptmx *os.File   // PTY master fd；nil 表示已关闭
	cmd  *exec.Cmd  // 用于 process kill 动作

	// 全量输出缓冲（线程安全，供 process log）
	aggBuf execBuf

	// 增量输出缓冲（供 process poll，每次读后清空）
	drainMu  sync.Mutex
	drainBuf bytes.Buffer

	exitCode int
	timedOut bool
}

// writeOutput 同时写入全量缓冲和增量缓冲
func (s *execSession) writeOutput(p []byte) {
	s.aggBuf.Write(p)
	s.drainMu.Lock()
	s.drainBuf.Write(p)
	s.drainMu.Unlock()
}

// drainOutput 返回并清空增量缓冲（自上次调用以来的新输出）
func (s *execSession) drainOutput() string {
	s.drainMu.Lock()
	defer s.drainMu.Unlock()
	out := s.drainBuf.String()
	s.drainBuf.Reset()
	return out
}

// fullOutput 返回全量历史输出（不清空）
func (s *execSession) fullOutput() string {
	return s.aggBuf.String()
}

// writeStdin 向 PTY master 写数据（等同于向进程的 stdin 输入）
func (s *execSession) writeStdin(data []byte) error {
	if s.ptmx == nil {
		return fmt.Errorf("pty already closed")
	}
	_, err := s.ptmx.Write(data)
	return err
}

var execRegistry = struct {
	mu       sync.Mutex
	sessions map[string]*execSession
}{sessions: make(map[string]*execSession)}

// ── 定时清理 ──────────────────────────────────────────────────────────────────

func init() {
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for range t.C {
			now := time.Now()

			// 清理过期 pending
			pendingStore.mu.Lock()
			for id, p := range pendingStore.data {
				if now.Sub(p.createdAt) > execPendingTTL {
					delete(pendingStore.data, id)
				}
			}
			pendingStore.mu.Unlock()

			// 清理已完成且过期的 session
			execRegistry.mu.Lock()
			for id, s := range execRegistry.sessions {
				select {
				case <-s.doneCh:
					if now.Sub(s.startedAt) > execSessionTTL {
						delete(execRegistry.sessions, id)
					}
				default:
				}
			}
			execRegistry.mu.Unlock()
		}
	}()
}

func newExecID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// ── handleExec：创建 pending，向前端推送审批确认框 ────────────────────────────

func handleExec(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Command    string            `json:"command"`
		Workdir    string            `json:"workdir"`
		Env        map[string]string `json:"env"`
		TimeoutSec int               `json:"timeout_sec"`
		YieldMs    int               `json:"yield_ms"`
		Background bool              `json:"background"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse exec args: %w", err)
	}
	if strings.TrimSpace(args.Command) == "" {
		return "", fmt.Errorf("command is required")
	}

	workdir := args.Workdir
	if workdir == "" {
		workdir = "."
	}

	// 构建 pending 记录
	pending := &execPending{
		id:         newExecID("ep_"),
		command:    args.Command,
		workdir:    workdir,
		env:        args.Env,
		timeoutSec: args.TimeoutSec,
		yieldMs:    args.YieldMs,
		background: args.Background,
		createdAt:  time.Now(),
	}

	pendingStore.mu.Lock()
	pendingStore.data[pending.id] = pending
	pendingStore.mu.Unlock()

	// 通过 InteractiveSender 向前端推送确认框
	sender := interactiveSenderFromCtx(ctx)
	if sender != nil {
		msg := fmt.Sprintf("即将执行以下命令：\n```\n%s\n```", args.Command)
		if workdir != "." {
			msg += fmt.Sprintf("\n工作目录：`%s`", workdir)
		}
		_ = sender("confirm", map[string]any{
			"message":       msg,
			"confirm_label": "确认执行",
			"cancel_label":  "取消",
		})
	}

	return execMarshal(map[string]any{
		"status":     "pending_approval",
		"pending_id": pending.id,
		"hint":       "Command is awaiting user approval. Stop and wait. After user confirms, call exec_run(pending_id) to execute. If user cancels, do not call exec_run.",
	}), nil
}

// ── handleExecRun：用户已确认，真正执行命令 ───────────────────────────────────

func handleExecRun(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		PendingID string `json:"pending_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse exec_run args: %w", err)
	}
	if args.PendingID == "" {
		return "", fmt.Errorf("pending_id is required")
	}

	// 取出并删除 pending 记录（一次性消费，防止重复执行）
	pendingStore.mu.Lock()
	pending, ok := pendingStore.data[args.PendingID]
	if ok {
		delete(pendingStore.data, args.PendingID)
	}
	pendingStore.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("pending_id %q not found (may have expired after 5 min, or already executed)", args.PendingID)
	}
	if time.Since(pending.createdAt) > execPendingTTL {
		return "", fmt.Errorf("approval expired (pending commands must be approved within 5 minutes)")
	}

	return runExecCommand(ctx, pending)
}

// ── runExecCommand：启动子进程（PTY），处理 yield 竞争 ────────────────────────

func runExecCommand(ctx context.Context, p *execPending) (string, error) {
	timeoutDur := time.Duration(config.Cfg.ToolExecTimeoutSec) * time.Second
	if p.timeoutSec > 0 {
		timeoutDur = time.Duration(p.timeoutSec) * time.Second
	}
	yieldMs := config.Cfg.ToolExecYieldMs
	if p.yieldMs > 0 {
		yieldMs = clampExecInt(p.yieldMs, 100, 120_000)
	}

	sess := &execSession{
		id:        newExecID("es_"),
		command:   p.command,
		startedAt: time.Now(),
		doneCh:    make(chan struct{}),
		readDone:  make(chan struct{}),
	}

	execRegistry.mu.Lock()
	execRegistry.sessions[sess.id] = sess
	execRegistry.mu.Unlock()

	// 子进程使用独立 context（不依赖 agent ctx，后台进程不受中止影响）
	cmdCtx, cancelCmd := context.WithTimeout(context.Background(), timeoutDur)

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", p.command)
	cmd.Dir = p.workdir

	// 覆盖 context 超时时的 kill 行为：杀进程组而非仅杀 bash
	// pty.StartWithSize 会设置 Setsid=true，新会话的 PGID == bash PID，
	// 所以 kill(-pgid, SIGKILL) 可清理 bash 及其所有子进程。
	cmd.Cancel = func() error {
		return killGroup(cmd)
	}

	if len(p.env) > 0 {
		env := os.Environ()
		for k, v := range p.env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	// 分配 PTY：bash 认为自己在真实终端里（支持进度条、颜色、交互程序）
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 220})
	if err != nil {
		cancelCmd()
		close(sess.readDone)
		close(sess.doneCh)
		execRegistry.mu.Lock()
		delete(execRegistry.sessions, sess.id)
		execRegistry.mu.Unlock()
		return "", fmt.Errorf("start command: %w", err)
	}
	sess.ptmx = ptmx
	sess.cmd = cmd

	// Read goroutine：ptmx → 双缓冲
	go func() {
		defer close(sess.readDone)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				sess.writeOutput(buf[:n])
			}
			if err != nil {
				// EIO/EOF 表示 slave 侧已关闭（进程退出），正常退出
				break
			}
		}
	}()

	// Wait goroutine：等待进程退出 → 关闭 ptmx → 等待 read goroutine 退出 → 关闭 doneCh
	go func() {
		defer cancelCmd()
		defer close(sess.doneCh)
		_ = cmd.Wait()
		ptmx.Close() // 触发 read goroutine 的 EIO/EOF
		<-sess.readDone // 确保所有输出已落入缓冲
		if cmdCtx.Err() == context.DeadlineExceeded {
			sess.timedOut = true
			sess.exitCode = -1
			return
		}
		if cmd.ProcessState != nil {
			sess.exitCode = cmd.ProcessState.ExitCode()
		} else {
			sess.exitCode = -1
		}
	}()

	// background：立即返回，不等待
	if p.background {
		return execMarshal(map[string]any{
			"status":     "running",
			"session_id": sess.id,
			"hint":       "Command started in background. Use process(action='poll', session_id=...) to check progress.",
		}), nil
	}

	// 竞争：进程完成 vs yield 定时器 vs agent ctx 取消（仅影响前台进程）
	yieldTimer := time.NewTimer(time.Duration(yieldMs) * time.Millisecond)
	defer yieldTimer.Stop()

	select {
	case <-sess.doneCh:
		return execDoneResult(sess), nil

	case <-yieldTimer.C:
		return execMarshal(map[string]any{
			"status":        "running",
			"session_id":    sess.id,
			"output_so_far": sess.drainOutput(),
			"hint":          fmt.Sprintf("Command still running after %dms. Use process(action='poll', session_id='%s') to check.", yieldMs, sess.id),
		}), nil

	case <-ctx.Done():
		_ = killGroup(cmd)
		return "", fmt.Errorf("exec cancelled by user")
	}
}

// ── 内部帮助函数 ──────────────────────────────────────────────────────────────

// killGroup 向 cmd 所在的进程组发送 SIGKILL，清理 bash 及其所有子进程。
// pty.StartWithSize 设置 Setsid=true，使 bash 成为新会话/进程组的组长，
// PGID == bash PID，用负数 PID 即可寻址整组。
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func execDoneResult(sess *execSession) string {
	elapsed := int(time.Since(sess.startedAt).Seconds())
	result := map[string]any{
		"status":      "done",
		"exit_code":   sess.exitCode,
		"output":      sess.fullOutput(),
		"elapsed_sec": elapsed,
	}
	if sess.timedOut {
		result["error"] = fmt.Sprintf("command timed out and was killed (timeout: %vs)", config.Cfg.ToolExecTimeoutSec)
	}
	return execMarshal(result)
}

func execMarshal(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func clampExecInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
