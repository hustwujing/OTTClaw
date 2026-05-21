// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

//go:build windows

package tool

import "fmt"

// sessionSendSignal on Windows：无 POSIX 进程组信号，统一用 Process.Kill()。
// SIGHUP 在 Windows 无对应语义，返回不支持错误。
func sessionSendSignal(sess *execSession, signal string) error {
	if sess.cmd == nil || sess.cmd.Process == nil {
		return nil
	}
	switch signal {
	case "SIGHUP":
		return fmt.Errorf("SIGHUP is not supported on Windows")
	case "", "SIGTERM", "SIGKILL", "SIGINT":
		return sess.cmd.Process.Kill()
	default:
		return fmt.Errorf("unsupported signal %q; valid: SIGTERM, SIGKILL, SIGINT", signal)
	}
}

// sessionForceKill on Windows：直接 Kill 进程。
func sessionForceKill(sess *execSession) {
	if sess.cmd != nil && sess.cmd.Process != nil {
		_ = sess.cmd.Process.Kill()
	}
}
