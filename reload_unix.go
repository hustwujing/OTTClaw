// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// startReloadSignalHandler 注册 SIGUSR1 信号处理器，收到信号时执行热重载回调。
// 用法：kill -USR1 <pid>
func startReloadSignalHandler(fn func()) {
	reload := make(chan os.Signal, 1)
	signal.Notify(reload, syscall.SIGUSR1)
	go func() {
		for range reload {
			fn()
		}
	}()
}
