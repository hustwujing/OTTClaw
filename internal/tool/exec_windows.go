// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

//go:build windows

package tool

import "os/exec"

// killGroup on Windows: 直接 Kill 进程（Windows 无 POSIX 进程组 / SIGKILL）。
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
