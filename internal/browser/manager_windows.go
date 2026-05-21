// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

//go:build windows

package browser

import "os/exec"

// killOrphans on Windows: no-op（pkill 不存在；Windows 服务管理器负责孤儿进程清理）。
func killOrphans() {}

// setPgid on Windows: no-op（Windows 无 POSIX 进程组概念）。
func setPgid(_ *exec.Cmd) {}

// sendTerm on Windows: 直接 Kill（Windows 无 SIGTERM）。
func sendTerm(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
