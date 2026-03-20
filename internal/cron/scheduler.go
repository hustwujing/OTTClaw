// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/cron/scheduler.go — 定时任务后台调度器
package cron

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

const tickInterval = 30 * time.Second

// AgentRunFunc 是运行 agent 的函数类型。
// cron 包不直接依赖 agent 包，由 main.go 在启动时注入（与 feishu 包的做法一致）。
// creatorSessionID 是创建该任务时所在的 web session，用于回写结果；jobName 供 writer 创建通知事件。
type AgentRunFunc func(ctx context.Context, userID, creatorSessionID, jobName, message string) error

var agentRunner AgentRunFunc

// SetAgentRunner 注入实际的 agent 运行函数（main.go 启动时调用一次）
func SetAgentRunner(fn AgentRunFunc) {
	agentRunner = fn
}

// Scheduler 后台定时任务调度器
type Scheduler struct {
	stop    chan struct{}
	running map[string]bool // 正在运行的 job ID 集合，防重入
	mu      sync.Mutex
}

// Default 全局默认调度器实例
var Default = &Scheduler{
	stop:    make(chan struct{}),
	running: make(map[string]bool),
}

// Start 启动后台调度 goroutine（每 30 秒 tick 一次）
func (s *Scheduler) Start() {
	go func() {
		// 服务启动后稍等 5 秒再首次 tick，确保 agent 已初始化完成
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-timer.C:
		case <-s.stop:
			timer.Stop()
			return
		}
		s.tick()

		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.tick()
			case <-s.stop:
				return
			}
		}
	}()
}

// Stop 停止调度器
func (s *Scheduler) Stop() {
	close(s.stop)
}

// tick 查询所有到期 job，为每个 job 启动独立 goroutine 执行
func (s *Scheduler) tick() {
	jobs, err := GetDueJobs()
	if err != nil {
		logger.Error("cron", "", "", "get due jobs failed", err, 0)
		return
	}
	for _, job := range jobs {
		s.mu.Lock()
		if s.running[job.ID] {
			s.mu.Unlock()
			continue // 防重入：上一次执行尚未结束
		}
		s.running[job.ID] = true
		s.mu.Unlock()

		go func(j storage.CronJob) {
			defer func() {
				s.mu.Lock()
				delete(s.running, j.ID)
				s.mu.Unlock()
			}()
			s.runJob(j)
		}(job)
	}
}

// runJob 为一个到期的 job 运行 agent，结果写回 creator session
func (s *Scheduler) runJob(job storage.CronJob) {
	if agentRunner == nil {
		logger.Error("cron", job.UserID, "", "agentRunner not set, skipping job", fmt.Errorf("agentRunner is nil"), 0)
		return
	}

	// 优先用创建任务时的 web session；若为空（旧任务或非 web 创建）则创建独立 cron session
	sessionID := job.CreatorSessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
		if err := storage.CreateSessionWithSource(sessionID, job.UserID, "cron"); err != nil {
			logger.Error("cron", job.UserID, "", "create fallback session failed", err, 0)
			return
		}
	}

	// 先计算并写入 next_run_at（防止重启后重复触发同一次 job）
	// 单次任务(DeleteAfterRun)先 disable 而非删除，确保崩溃后仍可恢复
	sched, parseErr := ParseSchedule(job.ScheduleJSON)
	if parseErr != nil {
		logger.Error("cron", job.UserID, sessionID,
			fmt.Sprintf("parse schedule for job %q failed", job.ID), parseErr, 0)
		return
	}
	now := time.Now()
	nextRunAt, calcErr := CalcNextRunAt(sched, &now, job.CreatedAt)
	if calcErr != nil {
		logger.Error("cron", job.UserID, sessionID,
			fmt.Sprintf("calc next run for job %q failed", job.ID), calcErr, 0)
	}
	// 先推进 next_run_at（重复任务）或 disable（单次任务），防止崩溃后重复触发
	if err := RecordRun(job.ID, nextRunAt, false); err != nil {
		logger.Error("cron", job.UserID, sessionID,
			fmt.Sprintf("record run for job %q failed", job.ID), err, 0)
	}
	if job.DeleteAfterRun {
		_ = UpdateJob(job.ID, job.UserID, map[string]any{"enabled": false})
	}

	// 再运行 agent（即使中途崩溃，next_run_at 已推进 / enabled 已关闭，不会重复触发）
	ctx := context.Background()
	runErr := agentRunner(ctx, job.UserID, sessionID, job.Name, job.Message)
	if runErr != nil {
		logger.Error("cron", job.UserID, sessionID,
			fmt.Sprintf("job %q agent run failed", job.Name), runErr, 0)
	} else {
		logger.Info("cron", job.UserID, sessionID,
			fmt.Sprintf("job %q (%s) completed", job.Name, job.ID), 0)
	}

	// agent 完成后，单次任务才真正删除
	if job.DeleteAfterRun {
		_ = RemoveJob(job.ID, job.UserID)
	}
}

// RunJobNow 立即（在新 goroutine 中）执行一次指定 job，供 cron tool 的 run action 调用
func (s *Scheduler) RunJobNow(job storage.CronJob) {
	go s.runJob(job)
}
