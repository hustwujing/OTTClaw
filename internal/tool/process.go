// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/process.go — process 工具实现（进程控制面板）
//
// process 是一个多动作工具，统一管理所有后台/前台 exec 会话：
//
//   list       列出所有会话（id、命令、状态、运行时长）
//   poll       等待新输出（增量轮询，返回自上次 poll 以来的新输出）
//   log        查看完整输出（支持 offset/limit 分页）
//   write      向 stdin 写入原始字节串（不自动换行）
//   submit     向 stdin 写入文本并追加 Enter（\r）
//   send-keys  发送命名按键（如 ctrl-c、enter、up 等）
//   paste      使用 bracketed paste 协议粘贴多行文本
//   kill       向进程组发送信号（默认 SIGTERM，可选 SIGKILL / SIGINT）
//   clear      清空增量缓冲（不影响全量历史）
//   remove     从注册表删除已完成的会话
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"syscall"
	"time"
)

// handleProcess 是 process 工具的入口，根据 action 路由到对应子处理函数
func handleProcess(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
		// poll
		Timeout int `json:"timeout"` // ms，默认 5000，上限 30000
		// log
		Offset int `json:"offset"` // 行偏移（0-indexed）
		Limit  int `json:"limit"`  // 最多返回行数，0 = 默认 200
		// write / submit / paste
		Text string `json:"text"`
		// send-keys
		Key string `json:"key"`
		// kill
		Signal string `json:"signal"` // "SIGTERM"（默认）/ "SIGKILL" / "SIGINT"
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse process args: %w", err)
	}

	switch args.Action {
	case "list":
		return processList()
	case "poll":
		return processPoll(args.SessionID, args.Timeout)
	case "log":
		return processLog(args.SessionID, args.Offset, args.Limit)
	case "write":
		return processWrite(args.SessionID, args.Text)
	case "submit":
		return processSubmit(args.SessionID, args.Text)
	case "send-keys":
		return processSendKeys(args.SessionID, args.Key)
	case "paste":
		return processPaste(args.SessionID, args.Text)
	case "kill":
		return processKill(args.SessionID, args.Signal)
	case "clear":
		return processClear(args.SessionID)
	case "remove":
		return processRemove(args.SessionID)
	default:
		return "", fmt.Errorf("unknown action %q; valid: list|poll|log|write|submit|send-keys|paste|kill|clear|remove", args.Action)
	}
}

// lookupSession 从注册表取出 session，不存在时返回描述性错误
func lookupSession(sessionID string) (*execSession, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	execRegistry.mu.Lock()
	sess, ok := execRegistry.sessions[sessionID]
	execRegistry.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session %q not found (may have expired after 2h, or not yet started)", sessionID)
	}
	return sess, nil
}

// ── list ──────────────────────────────────────────────────────────────────────

func processList() (string, error) {
	execRegistry.mu.Lock()
	defer execRegistry.mu.Unlock()

	type entry struct {
		ID         string `json:"id"`
		Command    string `json:"command"`
		Status     string `json:"status"`
		ElapsedSec int    `json:"elapsed_sec"`
		ExitCode   *int   `json:"exit_code,omitempty"`
	}

	list := make([]entry, 0, len(execRegistry.sessions))
	for _, s := range execRegistry.sessions {
		e := entry{
			ID:         s.id,
			Command:    s.command,
			ElapsedSec: int(time.Since(s.startedAt).Seconds()),
		}
		select {
		case <-s.doneCh:
			e.Status = "done"
			e.ExitCode = &s.exitCode
		default:
			e.Status = "running"
		}
		list = append(list, e)
	}

	b, _ := json.Marshal(list)
	return string(b), nil
}

// ── poll ──────────────────────────────────────────────────────────────────────

// processPoll 等待进程完成或 timeoutMs 到期，批量返回期间所有增量输出。
// 修复：不再在有新输出时立即返回（避免 npm install 等频繁输出的命令快速消耗 LLM 迭代次数）。
// 每次 poll 最多阻塞 timeoutMs，期间所有新输出在超时/完成时一次性返回给 LLM。
func processPoll(sessionID string, timeoutMs int) (string, error) {
	sess, err := lookupSession(sessionID)
	if err != nil {
		return "", err
	}

	// 已完成：直接返回
	select {
	case <-sess.doneCh:
		return execDoneResult(sess), nil
	default:
	}

	if timeoutMs <= 0 {
		timeoutMs = 10000 // 默认 10s（原为 5s）
	}
	if timeoutMs > 60000 {
		timeoutMs = 60000 // 最大 60s（原为 30s）
	}

	// 等待进程完成 or 超时；期间输出由 sess 内部双缓冲持续收集
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-sess.doneCh:
		return execDoneResult(sess), nil

	case <-timer.C:
		return execMarshal(map[string]any{
			"status":      "running",
			"session_id":  sess.id,
			"new_output":  sess.drainOutput(),
			"elapsed_sec": int(time.Since(sess.startedAt).Seconds()),
			"hint":        fmt.Sprintf("Command still running after %ds. Call process(action='poll') again to keep waiting.", timeoutMs/1000),
		}), nil
	}
}

// ── log ───────────────────────────────────────────────────────────────────────

// processLog 返回全量历史输出的指定行范围。
// offset: 起始行（0-indexed），limit: 最多返回行数（0=默认200）。
// 负 offset 表示从末尾倒数（tail 语义，如 offset=-200 limit=0 等同于 tail -200）。
func processLog(sessionID string, offset, limit int) (string, error) {
	sess, err := lookupSession(sessionID)
	if err != nil {
		return "", err
	}

	if limit <= 0 {
		limit = 200
	}

	full := sess.fullOutput()
	lines := strings.Split(full, "\n")
	total := len(lines)

	// 处理负数 offset（tail 语义）
	if offset < 0 {
		offset = total + offset
		if offset < 0 {
			offset = 0
		}
	}

	end := offset + limit
	if end > total {
		end = total
	}

	var selected []string
	if offset < total {
		selected = lines[offset:end]
	}

	isDone := false
	select {
	case <-sess.doneCh:
		isDone = true
	default:
	}

	return execMarshal(map[string]any{
		"session_id":  sess.id,
		"status":      map[bool]string{true: "done", false: "running"}[isDone],
		"total_lines": total,
		"offset":      offset,
		"lines":       selected,
		"elapsed_sec": int(time.Since(sess.startedAt).Seconds()),
	}), nil
}

// ── write ─────────────────────────────────────────────────────────────────────

// processWrite 向进程 stdin 写入原始文本（不添加换行）
func processWrite(sessionID, text string) (string, error) {
	sess, err := lookupSession(sessionID)
	if err != nil {
		return "", err
	}
	if err := sess.writeStdin([]byte(text)); err != nil {
		return "", fmt.Errorf("write stdin: %w", err)
	}
	return `"ok"`, nil
}

// ── submit ────────────────────────────────────────────────────────────────────

// processSubmit 向进程 stdin 写入文本并追加回车（\r），模拟用户输入后按 Enter
func processSubmit(sessionID, text string) (string, error) {
	sess, err := lookupSession(sessionID)
	if err != nil {
		return "", err
	}
	if err := sess.writeStdin([]byte(text + "\r")); err != nil {
		return "", fmt.Errorf("submit stdin: %w", err)
	}
	return `"ok"`, nil
}

// ── send-keys ─────────────────────────────────────────────────────────────────

// keyMap 将按键名映射到对应的字节序列
var keyMap = map[string]string{
	"ctrl-a":    "\x01",
	"ctrl-b":    "\x02",
	"ctrl-c":    "\x03",
	"ctrl-d":    "\x04",
	"ctrl-e":    "\x05",
	"ctrl-f":    "\x06",
	"ctrl-k":    "\x0b",
	"ctrl-l":    "\x0c",
	"ctrl-n":    "\x0e",
	"ctrl-p":    "\x10",
	"ctrl-r":    "\x12",
	"ctrl-u":    "\x15",
	"ctrl-w":    "\x17",
	"ctrl-z":    "\x1a",
	"enter":     "\r",
	"return":    "\r",
	"tab":       "\t",
	"escape":    "\x1b",
	"esc":       "\x1b",
	"backspace": "\x7f",
	"delete":    "\x1b[3~",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"home":      "\x1b[H",
	"end":       "\x1b[F",
	"page-up":   "\x1b[5~",
	"page-down": "\x1b[6~",
	"f1":        "\x1bOP",
	"f2":        "\x1bOQ",
	"f3":        "\x1bOR",
	"f4":        "\x1bOS",
	"f5":        "\x1b[15~",
	"f6":        "\x1b[17~",
	"f7":        "\x1b[18~",
	"f8":        "\x1b[19~",
	"f9":        "\x1b[20~",
	"f10":       "\x1b[21~",
	"f11":       "\x1b[23~",
	"f12":       "\x1b[24~",
}

// processSendKeys 将按键名转换为字节序列后写入 stdin
func processSendKeys(sessionID, key string) (string, error) {
	sess, err := lookupSession(sessionID)
	if err != nil {
		return "", err
	}

	seq, ok := keyMap[strings.ToLower(key)]
	if !ok {
		keys := make([]string, 0, len(keyMap))
		for k := range keyMap {
			keys = append(keys, k)
		}
		return "", fmt.Errorf("unknown key %q; valid keys: %s", key, strings.Join(keys, ", "))
	}

	if err := sess.writeStdin([]byte(seq)); err != nil {
		return "", fmt.Errorf("send-keys: %w", err)
	}
	return `"ok"`, nil
}

// ── paste ─────────────────────────────────────────────────────────────────────

// processPaste 使用 bracketed paste 协议将多行文本粘贴到 stdin，
// 防止程序将换行符误解为命令提交。
func processPaste(sessionID, text string) (string, error) {
	sess, err := lookupSession(sessionID)
	if err != nil {
		return "", err
	}
	// bracketed paste: ESC[200~ + text + ESC[201~
	payload := "\x1b[200~" + text + "\x1b[201~"
	if err := sess.writeStdin([]byte(payload)); err != nil {
		return "", fmt.Errorf("paste: %w", err)
	}
	return `"ok"`, nil
}

// ── kill ──────────────────────────────────────────────────────────────────────

// processKill 向进程组发送指定信号（默认 SIGTERM）
func processKill(sessionID, signal string) (string, error) {
	sess, err := lookupSession(sessionID)
	if err != nil {
		return "", err
	}
	if sess.cmd == nil || sess.cmd.Process == nil {
		return "", fmt.Errorf("process not available (already exited?)")
	}

	var sig syscall.Signal
	switch strings.ToUpper(signal) {
	case "", "SIGTERM":
		sig = syscall.SIGTERM
	case "SIGKILL":
		sig = syscall.SIGKILL
	case "SIGINT":
		sig = syscall.SIGINT
	case "SIGHUP":
		sig = syscall.SIGHUP
	default:
		return "", fmt.Errorf("unsupported signal %q; valid: SIGTERM, SIGKILL, SIGINT, SIGHUP", signal)
	}

	err = syscall.Kill(-sess.cmd.Process.Pid, sig)
	if err != nil {
		return "", fmt.Errorf("kill: %w", err)
	}
	return `"ok"`, nil
}

// ── clear ─────────────────────────────────────────────────────────────────────

// processClear 清空增量缓冲（不影响 aggBuf 全量历史）
func processClear(sessionID string) (string, error) {
	sess, err := lookupSession(sessionID)
	if err != nil {
		return "", err
	}
	sess.drainOutput() // drain and discard
	return `"ok"`, nil
}

// ── remove ────────────────────────────────────────────────────────────────────

// processRemove 从注册表删除会话；运行中的会话会被先 kill 再删除
func processRemove(sessionID string) (string, error) {
	sess, err := lookupSession(sessionID)
	if err != nil {
		return "", err
	}

	// 若仍在运行，先 kill
	select {
	case <-sess.doneCh:
	default:
		if sess.cmd != nil && sess.cmd.Process != nil {
			_ = syscall.Kill(-sess.cmd.Process.Pid, syscall.SIGKILL)
		}
	}

	execRegistry.mu.Lock()
	delete(execRegistry.sessions, sessionID)
	execRegistry.mu.Unlock()

	return `"ok"`, nil
}
