// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

//go:build !windows

package browser

import (
	"os/exec"
	"syscall"
	"time"
)

// killOrphans 杀掉上次遗留的同名 browser-server 进程，确保端口空闲。
// pkill 仅在 Unix 系统可用，Windows 版为空实现（见 manager_windows.go）。
func killOrphans() {
	cmd := exec.Command("pkill", "-f", "node.*browser-server/server\\.js")
	if err := cmd.Run(); err == nil {
		// 找到并杀掉了残留进程，等端口释放
		time.Sleep(300 * time.Millisecond)
	}
}

// setPgid 设置 Setpgid 使子进程成为独立进程组组长。
func setPgid(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// sendTerm 向进程组发送 SIGTERM。
func sendTerm(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}
