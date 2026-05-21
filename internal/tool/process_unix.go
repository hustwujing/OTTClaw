// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

//go:build !windows

package tool

import (
	"fmt"
	"strings"
	"syscall"
)

// sessionSendSignal 向进程组发送指定信号（Unix 实现）。
// 负数 PID 寻址整个进程组（PGID == bash PID，由 pty.StartWithSize 的 Setsid=true 保证）。
func sessionSendSignal(sess *execSession, signal string) error {
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
		return fmt.Errorf("unsupported signal %q; valid: SIGTERM, SIGKILL, SIGINT, SIGHUP", signal)
	}
	return syscall.Kill(-sess.cmd.Process.Pid, sig)
}

// sessionForceKill 强制杀死进程组（SIGKILL），用于 remove 时清理残留进程。
func sessionForceKill(sess *execSession) {
	if sess.cmd != nil && sess.cmd.Process != nil {
		_ = syscall.Kill(-sess.cmd.Process.Pid, syscall.SIGKILL)
	}
}
