// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

//go:build windows

package main

// startReloadSignalHandler on Windows：SIGUSR1 不可用，热重载只能通过 HTTP API 触发。
func startReloadSignalHandler(_ func()) {}
