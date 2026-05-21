// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/cron/langfuse_scorer.go — Langfuse Task Unit 评估：条件 A 定时扫描
//
// 每 5 分钟扫描一次，找出静默超过 LANGFUSE_SCORER_IDLE_MINUTES 的 session，
// 异步调用 ScoreSession 进行评估。
package cron

import (
	"sync"
	"time"

	"OTTClaw/config"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

// ScorerFunc 是调用 agent.ScoreSession 的函数类型。
// cron 包不直接依赖 agent 包，由 main.go 注入。
type ScorerFunc func(sessionID, userID string)

var scorerFn ScorerFunc

// SetScorerFunc 注入 ScoreSession 函数（main.go 启动时调用一次）
func SetScorerFunc(fn ScorerFunc) {
	scorerFn = fn
}

// LangfuseScorerScheduler 负责定时扫描并触发条件 A 的评估任务
type LangfuseScorerScheduler struct {
	stop     chan struct{}
	stopOnce sync.Once
}

// DefaultLangfuseScorer 全局默认 scorer 调度器实例
var DefaultLangfuseScorer = &LangfuseScorerScheduler{
	stop: make(chan struct{}),
}

const scorerTickInterval = 5 * time.Minute

// Start 启动后台定时扫描 goroutine
func (s *LangfuseScorerScheduler) Start() {
	go func() {
		// 启动时等待 10 秒，确保 agent 完全初始化
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-timer.C:
		case <-s.stop:
			timer.Stop()
			return
		}

		s.scan()

		ticker := time.NewTicker(scorerTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.scan()
			case <-s.stop:
				return
			}
		}
	}()
}

// Stop 停止调度器，幂等（多次调用安全）。
func (s *LangfuseScorerScheduler) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// scan 扫描所有满足条件 A 的 session 并异步触发评估
func (s *LangfuseScorerScheduler) scan() {
	if !config.Cfg.LangfuseEnabled || scorerFn == nil {
		return
	}

	idleMinutes := config.Cfg.LangfuseScorerIdleMinutes
	if idleMinutes <= 0 {
		idleMinutes = 15
	}
	idleCutoff := time.Now().Add(-time.Duration(idleMinutes) * time.Minute)

	sessions, err := storage.GetIdleSessionsWithPendingScore(idleCutoff)
	if err != nil {
		logger.Error("langfuse-scorer", "", "", "scan idle sessions failed", err, 0)
		return
	}

	for _, sess := range sessions {
		logger.Info("langfuse-scorer", sess.UserID, sess.SessionID,
			"[scorer] condition-A triggered: session idle, starting ScoreSession", 0)
		go scorerFn(sess.SessionID, sess.UserID)
	}

	if len(sessions) == 0 {
		logger.Info("langfuse-scorer", "", "", "[scorer] condition-A scan: no idle sessions to score", 0)
	}
}

// itoa 简单整数转字符串，避免引入 strconv
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
