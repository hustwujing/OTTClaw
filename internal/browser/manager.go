// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/browser/manager.go — Node.js Playwright 子进程生命周期管理
//
// 启动流程：
//  1. Start() 执行 node browser-server/server.js
//  2. 扫描子进程 stdout 等待 "Listening on" 就绪信号（最多 30 秒）
//  3. 就绪后 Go 侧可通过 BaseURL() 拿到 HTTP 地址，client.go 发请求
//
// 关闭流程：
//  - Stop() 向子进程发 SIGTERM，等待 3 秒后强杀
package browser

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"OTTClaw/config"
	"OTTClaw/internal/logger"
)

// Manager 管理 Node.js Playwright 子进程
type Manager struct {
	cmd     *exec.Cmd
	baseURL string
	running atomic.Bool
	mu      sync.Mutex
}

var Default = &Manager{}

// killOrphans 杀掉上次遗留的同名 browser-server 进程，确保端口空闲
func killOrphans() {
	cmd := exec.Command("pkill", "-f", "node.*browser-server/server\\.js")
	if err := cmd.Run(); err == nil {
		// 找到并杀掉了残留进程，等端口释放
		time.Sleep(300 * time.Millisecond)
	}
}

// Start 启动 Node.js 子进程并等待就绪（最多 30 秒）
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running.Load() {
		return nil
	}

	// 先清理上次可能残留的孤儿进程，避免端口被占导致 EADDRINUSE
	killOrphans()

	port := config.Cfg.BrowserServerPort
	script := config.Cfg.BrowserServerScript

	headless := "true"
	if !config.Cfg.BrowserHeadless {
		headless = "false"
	}

	cmd := exec.Command("node", script)
	cmd.Env = append(cmd.Environ(),
		fmt.Sprintf("PORT=%s", port),
		fmt.Sprintf("OUTPUT_DIR=%s", config.Cfg.OutputDir),
		fmt.Sprintf("BROWSER_HEADLESS=%s", headless),
	)
	// 使进程组与父进程独立，便于 SIGTERM 精确杀进程
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// 捕获 stdout 检测就绪
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("browser: stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout pipe

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("browser: start node: %w", err)
	}

	m.cmd = cmd
	ready := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			logger.Info("browser", "", "", "[node] "+line, 0)
			if strings.Contains(line, "Listening on") {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}
	}()

	// 监视子进程退出
	go func() {
		_ = cmd.Wait()
		m.running.Store(false)
		logger.Warn("browser", "", "", "Node.js browser-server process exited", 0)
	}()

	select {
	case <-ready:
		m.baseURL = fmt.Sprintf("http://127.0.0.1:%s", port)
		m.running.Store(true)
		logger.Info("browser", "", "", "browser-server ready at "+m.baseURL, 0)
		return nil
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return fmt.Errorf("browser: server did not become ready within 30s")
	}
}

// Stop 发 SIGTERM，等待 3 秒后强杀
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil || m.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-m.cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = m.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = m.cmd.Process.Kill()
	}
	m.running.Store(false)
	logger.Info("browser", "", "", "browser-server stopped", 0)
}

// IsRunning 返回子进程是否存活
func (m *Manager) IsRunning() bool {
	return m.running.Load()
}

// BaseURL 返回 Node.js HTTP 服务地址，如 http://127.0.0.1:9222
func (m *Manager) BaseURL() string {
	return m.baseURL
}
