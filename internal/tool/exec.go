// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/exec.go — exec / exec_run 工具实现（PTY 版）
//
// 执行流程（两步 + 可选轮询）：
//
//  1. exec(command, ...)
//     创建 pending 记录 → 通过 InteractiveSender 向前端推送确认框
//     → 返回 {status:"pending_approval", pending_id:"..."}
//     LLM 收到后必须停止，等待用户在下一轮点击确认/取消。
//
//  2. 用户点击「确认执行」→ 文本作为下一条用户消息发回
//     LLM 调用 exec_run(pending_id) 真正执行命令
//     → 命令在 yield_ms 内完成：同步返回完整输出
//     → 超过 yield_ms 仍在运行：后台化，返回 session_id
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
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"OTTClaw/config"
	"OTTClaw/internal/channel"
)

// ansiEscape 匹配 ANSI 转义序列，用于清理 PTY 输出中的终端控制字符。
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// absPathRe 从任意文本行中提取绝对路径候选。
// 支持 Unix/macOS 风格（/tmp/chart.png）和 Windows 风格（C:\tmp\chart.png）。
// 例：能从 "图片已保存到: /tmp/chart.png" 中提取 "/tmp/chart.png"。
var absPathRe = regexp.MustCompile(`(?:/[^\s'"` + "`" + `]+|[A-Za-z]:[/\\][^\s'"` + "`" + `]+)`)

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

	ptmx *os.File  // PTY master fd；nil 表示已关闭
	cmd  *exec.Cmd // 用于 process kill 动作

	// 全量输出缓冲（线程安全，供 process log）
	aggBuf execBuf

	// 增量输出缓冲（供 process poll，每次读后清空）
	drainMu  sync.Mutex
	drainBuf bytes.Buffer

	exitCode int
	timedOut bool

	// AGENT_REGISTER_FILE 路径；启动前创建，exec 结束后读取并删除
	regFile string

	// AGENT_OUTPUT_DIR 路径；session 级隔离目录，exec 结束后扫描并搬空
	outputDir string
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

			// 清理已完成且过期的 session（同时清理孤立的注册文件）
			execRegistry.mu.Lock()
			for id, s := range execRegistry.sessions {
				select {
				case <-s.doneCh:
					if now.Sub(s.startedAt) > execSessionTTL {
						if s.regFile != "" {
							_ = os.Remove(s.regFile)
						}
						if s.outputDir != "" {
							_ = os.RemoveAll(s.outputDir)
						}
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

	// 子 agent 后台无人值守，或渠道不支持交互式确认框（微信/飞书等），直接执行。
	// 安全性：用户在 spawn_subagent 时已授权子 agent 执行，微信/飞书等渠道无法弹确认框。
	if taskIDFromCtx(ctx) > 0 || channel.ExecAutoApproveFromCtx(ctx) {
		return runExecCommand(ctx, pending)
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
			"pending_id":    pending.id,
		})
	}

	// 使用具名结构体而非 map，确保 JSON 字段顺序固定：
	// status 和 pending_id 必须排在 command（可能非常长）之前，
	// 否则 DB 截断（TOOL_RESULT_MAX_DB_BYTES）会在 pending_id 出现之前截断内容，
	// 导致下轮对话中 LLM 无法读取 pending_id 并产生幻觉。
	type pendingApprovalResult struct {
		Status    string `json:"status"`
		PendingID string `json:"pending_id"`
		Command   string `json:"command"`
		Hint      string `json:"hint"`
	}
	b, _ := json.Marshal(pendingApprovalResult{
		Status:    "pending_approval",
		PendingID: pending.id,
		Command:   args.Command,
		Hint:      "Command is awaiting user approval. Stop and wait. After user confirms, call exec_run(pending_id) to execute. If user cancels, do not call exec_run.",
	})
	return string(b), nil
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

	// 启动前创建 session 级隔离输出目录（user + session 双隔离）
	// 路径含随机 session ID，不同用户、不同会话互不可见
	outputDir := filepath.Join(os.TempDir(), "agent_out_"+sess.id)
	if err := os.MkdirAll(outputDir, 0o755); err == nil {
		sess.outputDir = outputDir
	}

	// 启动前创建注册文件，脚本通过 $AGENT_REGISTER_FILE 主动追加生成的文件路径
	regFile := filepath.Join(os.TempDir(), "agent_reg_"+sess.id)
	if f, err := os.Create(regFile); err == nil {
		f.Close()
		sess.regFile = regFile
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

	// 始终注入 AGENT_OUTPUT_DIR 和 AGENT_REGISTER_FILE（无论 p.env 是否为空）
	env := os.Environ()
	if sess.outputDir != "" {
		env = append(env, "AGENT_OUTPUT_DIR="+sess.outputDir)
	}
	if sess.regFile != "" {
		env = append(env, "AGENT_REGISTER_FILE="+sess.regFile)
	}
	for k, v := range p.env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

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

	// Wait goroutine：等待进程退出 → 等待 read goroutine 耗尽缓冲 → 关闭 ptmx → 关闭 doneCh
	//
	// 注意顺序：必须先 <-sess.readDone 再 ptmx.Close()。
	// 进程退出后，PTY slave 侧关闭，内核 PTY buffer 里可能还有未读数据；
	// 若在 read goroutine 读完之前调用 ptmx.Close()，会中断正在进行的 Read，
	// 导致最后一批输出（如 Python print(output_path)）丢失，output 变成空字符串。
	// 加 3s 超时兜底，防止 read goroutine 因异常无法退出时永远阻塞。
	go func() {
		defer cancelCmd()
		defer close(sess.doneCh)
		_ = cmd.Wait()
		// 等 read goroutine 自然因 slave 关闭得到 EIO 并退出，确保全部输出已落盘
		select {
		case <-sess.readDone:
		case <-time.After(3 * time.Second):
		}
		ptmx.Close() // 全部数据已读完，现在关闭 PTY master
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
		return execDoneResult(sess, userIDFromCtx(ctx)), nil

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

func execDoneResult(sess *execSession, userID string) string {
	elapsed := int(time.Since(sess.startedAt).Seconds())
	output := sess.fullOutput()
	// 空输出时注入 exit code 标注，防止 LLM 因空工具结果误判任务已结束。
	// [exec:exit=N] 仅含技术数值，无自然语言词汇，不会被 LLM 学习模仿。
	if strings.TrimSpace(output) == "" {
		output = fmt.Sprintf("[exec:exit=%d]", sess.exitCode)
	}
	result := map[string]any{
		"status":      "done",
		"exit_code":   sess.exitCode,
		"output":      output,
		"elapsed_sec": elapsed,
	}
	if sess.timedOut {
		result["error"] = fmt.Sprintf("command timed out and was killed (timeout: %vs)", config.Cfg.ToolExecTimeoutSec)
	}
	outputAbs, _ := filepath.Abs(config.Cfg.OutputDir)
	seen := make(map[string]bool)     // web 路径去重
	seenFile := make(map[string]bool) // 文件名去重
	var imgs []string
	var genFiles []execGeneratedFile

	// ── 优先一：扫描 AGENT_OUTPUT_DIR（session 级隔离目录，最可靠）──────────────
	if sess.outputDir != "" {
		dirImgs, dirFiles := scanAgentOutputDir(sess.outputDir, outputAbs, userID)
		for _, u := range dirImgs {
			if !seen[u] {
				seen[u] = true
				imgs = append(imgs, u)
			}
		}
		for _, f := range dirFiles {
			seenFile[f.Name] = true
			genFiles = append(genFiles, f)
		}
	}

	// ── 优先二：读取 AGENT_REGISTER_FILE（文件保存在其他位置时手动注册）─────────
	if sess.regFile != "" {
		regImgs, regFiles := processRegFile(sess.regFile, outputAbs, userID)
		for _, u := range regImgs {
			if !seen[u] {
				seen[u] = true
				imgs = append(imgs, u)
			}
		}
		for _, f := range regFiles {
			if !seenFile[f.Name] {
				seenFile[f.Name] = true
				genFiles = append(genFiles, f)
			}
		}
	}

	// ── 兜底一：扫描 output 目录新增图片（脚本直接写 output/ 的场景）────────────
	for _, u := range scanNewOutputImages(sess.startedAt) {
		if !seen[u] {
			seen[u] = true
			imgs = append(imgs, u)
		}
	}

	// ── 兜底二：解析 stdout 打印的路径（未使用 AGENT_REGISTER_FILE 的旧脚本）────
	stdoutImgs, stdoutFiles := scanFilesFromOutput(sess.fullOutput(), sess.startedAt, userID)
	for _, u := range stdoutImgs {
		if !seen[u] {
			seen[u] = true
			imgs = append(imgs, u)
		}
	}
	// 兜底二 genFiles 去重（避免与上游结果重复）
	for _, f := range stdoutFiles {
		if !seenFile[f.Name] {
			seenFile[f.Name] = true
			genFiles = append(genFiles, f)
		}
	}

	if len(imgs) > 0 {
		result["generatedImages"] = imgs
	}
	if len(genFiles) > 0 {
		result["generatedFiles"] = genFiles
	}
	return execMarshal(result)
}

// scanNewOutputImages 扫描 output 目录，返回在 since 之后新建或修改的图片文件的 web 路径列表。
// 留 1s 缓冲以应对文件系统 mtime 精度为 1s 的场景（避免漏检同秒内生成的文件）。
func scanNewOutputImages(since time.Time) []string {
	outputAbs, err := filepath.Abs(config.Cfg.OutputDir)
	if err != nil {
		return nil
	}
	threshold := since.Add(-time.Second)
	var images []string
	_ = filepath.Walk(outputAbs, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !isImagePath(path) {
			return nil
		}
		if !info.ModTime().Before(threshold) {
			rel := strings.TrimPrefix(path, outputAbs)
			rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
			images = append(images, "/"+config.Cfg.OutputDir+rel)
		}
		return nil
	})
	return images
}

// execGeneratedFile 描述 exec 运行期间自动检测到的非图片生成文件。
type execGeneratedFile struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
}

// scanFilesFromOutput 解析 exec 标准输出，查找其中打印的文件绝对路径。
//
// 匹配条件：每行去除 ANSI 转义、首尾空白和 CR 后，满足：
//   - 是绝对路径
//   - 文件存在且 mtime 不早于 since-1s（exec 开始时间带 1s 缓冲）
//
// 图片文件（.png/.jpg/…）：若不在 output 目录内，移动到 output/<userID>/<bucket>/，
// 再通过 RegisterFileDownload 获取 webURL，追加到返回的 images 列表。
// 所有文件：注册临时下载 token，追加到返回的 files 列表。
func scanFilesFromOutput(output string, since time.Time, userID string) (images []string, files []execGeneratedFile) {
	outputAbs, _ := filepath.Abs(config.Cfg.OutputDir)
	threshold := since.Add(-time.Second)
	seen := make(map[string]bool)

	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		line = ansiEscape.ReplaceAllString(line, "")

		// 从行内提取所有绝对路径候选（处理 "图片已保存到: /tmp/xxx.png" 等前缀场景）
		for _, candidate := range absPathRe.FindAllString(line, -1) {
			// 去掉末尾可能误匹配的标点（如句号、冒号、括号）
			candidate = strings.TrimRight(candidate, ".,;:!?)>")
			if seen[candidate] {
				continue
			}
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() {
				continue
			}
			if info.ModTime().Before(threshold) {
				continue
			}
			seen[candidate] = true

			absPath := candidate

			// 图片且不在 output 目录：按分桶规则移动到 output/<userID>/<bucket>/
			if isImagePath(absPath) && outputAbs != "" && !strings.HasPrefix(absPath, outputAbs) {
				if dest, moveErr := moveFileToOutputBucket(absPath, outputAbs, userID); moveErr == nil {
					absPath = dest
				}
			}

			dlURL, webURL, regErr := RegisterFileDownload(absPath)
			if regErr != nil {
				continue
			}

			files = append(files, execGeneratedFile{
				Name:        filepath.Base(candidate),
				DownloadURL: dlURL,
			})
			if webURL != "" {
				images = append(images, webURL)
			}
		}
	}
	return
}

// scanAgentOutputDir 扫描 AGENT_OUTPUT_DIR 目录中的所有文件，按分桶规则移动到 output/<userID>/<bucket>/。
// 扫描完成后删除整个目录（所有文件已移走，目录本身已空）。
// 支持子目录递归扫描，适合脚本生成多个文件的场景。
func scanAgentOutputDir(agentOutputDir, outputAbs, userID string) (images []string, files []execGeneratedFile) {
	if agentOutputDir == "" {
		return
	}
	defer os.RemoveAll(agentOutputDir)

	_ = filepath.Walk(agentOutputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		absPath := path
		// 移动到 output 分桶
		if outputAbs != "" {
			if dest, moveErr := moveFileToOutputBucket(absPath, outputAbs, userID); moveErr == nil {
				absPath = dest
			}
		}
		dlURL, webURL, regErr := RegisterFileDownload(absPath)
		if regErr != nil {
			return nil
		}
		files = append(files, execGeneratedFile{
			Name:        info.Name(),
			DownloadURL: dlURL,
		})
		if webURL != "" {
			images = append(images, webURL)
		}
		return nil
	})
	return
}

// processRegFile 读取 AGENT_REGISTER_FILE，处理其中注册的所有文件路径。
// 每行一个绝对路径，支持图片和任意文件类型。
// 不在 output 目录内的文件按分桶规则移动到 output/<userID>/<bucket>/；
// 读取完成后删除注册文件。
func processRegFile(regFile, outputAbs, userID string) (images []string, files []execGeneratedFile) {
	defer os.Remove(regFile)

	data, err := os.ReadFile(regFile)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return
	}

	seen := make(map[string]bool)
	for _, raw := range strings.Split(string(data), "\n") {
		path := strings.TrimSpace(raw)
		if path == "" || seen[path] {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		seen[path] = true

		absPath := path
		// 不在 output 目录：按分桶规则移动
		if outputAbs != "" && !strings.HasPrefix(absPath, outputAbs) {
			if dest, moveErr := moveFileToOutputBucket(absPath, outputAbs, userID); moveErr == nil {
				absPath = dest
			}
		}

		dlURL, webURL, regErr := RegisterFileDownload(absPath)
		if regErr != nil {
			continue
		}

		files = append(files, execGeneratedFile{
			Name:        filepath.Base(path), // 使用原始文件名
			DownloadURL: dlURL,
		})
		if webURL != "" {
			images = append(images, webURL)
		}
	}
	return
}

// moveFileToOutputBucket 将文件移动到 outputAbs/<userID>/<bucket>/ 目录，返回目标绝对路径。
// bucket 取文件名 MD5 的第二位十六进制字符（大写），与 write_output.go 分桶规则一致。
// 优先使用 os.Rename（同一文件系统零拷贝），跨文件系统时降级为复制后删除源文件。
// 若目标同名文件已存在，加毫秒时间戳后缀避免覆盖。
func moveFileToOutputBucket(srcPath, outputAbs, userID string) (string, error) {
	if userID == "" {
		userID = "_shared"
	}
	sum := md5.Sum([]byte(filepath.Base(srcPath)))
	bucketDir := strings.ToUpper(string(fmt.Sprintf("%x", sum)[1]))
	destDir := filepath.Join(outputAbs, userID, bucketDir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	baseName := filepath.Base(srcPath)
	destPath := filepath.Join(destDir, baseName)
	if _, err := os.Stat(destPath); err == nil {
		// 同名文件已存在，加时间戳后缀
		ext := filepath.Ext(baseName)
		name := strings.TrimSuffix(baseName, ext)
		destPath = filepath.Join(destDir, fmt.Sprintf("%s_%d%s", name, time.Now().UnixMilli(), ext))
	}
	// 优先 Rename（同一文件系统零拷贝）
	if err := os.Rename(srcPath, destPath); err == nil {
		return destPath, nil
	}
	// 跨文件系统降级为复制后删除
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		return "", err
	}
	_ = os.Remove(srcPath)
	return destPath, nil
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
