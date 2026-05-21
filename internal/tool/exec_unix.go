// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

//go:build !windows

package tool

import (
	"os/exec"
	"syscall"
)

// killGroup 向 cmd 所在的进程组发送 SIGKILL，清理 bash 及其所有子进程。
// pty.StartWithSize 设置 Setsid=true，使 bash 成为新会话/进程组的组长，
// PGID == bash PID，用负数 PID 即可寻址整组。
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
