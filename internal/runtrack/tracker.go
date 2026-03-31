// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/runtrack/tracker.go — 全局 agent 运行追踪器
//
// 轻量内存追踪，记录当前正在执行的所有 agent 运行（web / feishu / cron / subagent）。
// 不依赖 DB，进程重启后自然清零，语义对齐"实时并发数"。
// 与 cron.Scheduler.running 思路一致，但覆盖所有 runtime 来源。
package runtrack

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// RunEntry 单次 agent 运行的内部元数据
type RunEntry struct {
	RunID     string
	Runtime   string // "web" | "feishu" | "cron" | "subagent"
	UserID    string
	SessionID string
	StartedAt time.Time
}

// RunInfo 对外暴露的运行快照（含 elapsed_ms）
type RunInfo struct {
	RunID     string    `json:"run_id"`
	Runtime   string    `json:"runtime"`
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
	StartedAt time.Time `json:"started_at"`
	ElapsedMs int64     `json:"elapsed_ms"`
}

// Summary 各 runtime 并发数汇总
type Summary struct {
	Total    int `json:"total"`
	Web      int `json:"web"`
	Feishu   int `json:"feishu"`
	Cron     int `json:"cron"`
	Subagent int `json:"subagent"`
}

// Tracker 内存运行追踪器，并发安全
type Tracker struct {
	mu      sync.Mutex
	running map[string]*RunEntry
}

// Default 全局默认实例
var Default = &Tracker{
	running: make(map[string]*RunEntry),
}

// Register 注册一次 agent 运行，返回注销函数（供 defer 调用）。
// 调用示例：defer runtrack.Default.Register("web", userID, sessionID)()
func (t *Tracker) Register(runtime, userID, sessionID string) func() {
	runID := uuid.New().String()
	t.mu.Lock()
	t.running[runID] = &RunEntry{
		RunID:     runID,
		Runtime:   runtime,
		UserID:    userID,
		SessionID: sessionID,
		StartedAt: time.Now(),
	}
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		delete(t.running, runID)
		t.mu.Unlock()
	}
}

// Snapshot 返回当前所有运行的快照列表（按 started_at 正序）
func (t *Tracker) Snapshot() []RunInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	result := make([]RunInfo, 0, len(t.running))
	for _, e := range t.running {
		result = append(result, RunInfo{
			RunID:     e.RunID,
			Runtime:   e.Runtime,
			UserID:    e.UserID,
			SessionID: e.SessionID,
			StartedAt: e.StartedAt,
			ElapsedMs: now.Sub(e.StartedAt).Milliseconds(),
		})
	}
	return result
}

// GetSummary 返回各 runtime 并发数汇总
func (t *Tracker) GetSummary() Summary {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := Summary{}
	for _, e := range t.running {
		s.Total++
		switch e.Runtime {
		case "web":
			s.Web++
		case "feishu":
			s.Feishu++
		case "cron":
			s.Cron++
		case "subagent":
			s.Subagent++
		}
	}
	return s
}
