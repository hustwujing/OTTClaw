// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/cron/scheduler.go — 定时任务后台调度器
package cron

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

const (
	tickInterval = 30 * time.Second
	// cronJobTimeout 单个 cron job agent 运行的最长时间，对齐 subagentTimeout。
	// 超时后 context 被取消，agent.Run 返回，goroutine 正常退出，running 条目自动清除。
	cronJobTimeout = 30 * time.Minute
	// cronStaleThreshold 是 reapStale 触发强制清除的门槛。
	// 正常情况下 context 超时已使 goroutine 退出；此阈值提供额外保障，
	// 防止极端场景（goroutine 忽略 ctx 取消）导致 running 条目永久残留。
	cronStaleThreshold = cronJobTimeout + 5*time.Minute
)

// AgentRunFunc 是运行 agent 的函数类型。
// cron 包不直接依赖 agent 包，由 main.go 在启动时注入（与 feishu 包的做法一致）。
// creatorSessionID 是创建该任务时所在的 web session，用于回写结果；jobName 供 writer 创建通知事件。
type AgentRunFunc func(ctx context.Context, userID, creatorSessionID, jobName, message string) error

var agentRunner AgentRunFunc

// SetAgentRunner 注入实际的 agent 运行函数（main.go 启动时调用一次）
func SetAgentRunner(fn AgentRunFunc) {
	agentRunner = fn
}

// cronRunEntry 记录单次 job 运行的元数据，用于泄漏检测与强制取消。
type cronRunEntry struct {
	startedAt time.Time
	cancel    context.CancelFunc
}

// RunningJobInfo 正在执行的 job 快照，供外部状态查询使用。
type RunningJobInfo struct {
	JobID     string    `json:"job_id"`
	StartedAt time.Time `json:"started_at"`
	ElapsedMs int64     `json:"elapsed_ms"`
}

// Scheduler 后台定时任务调度器
type Scheduler struct {
	stop    chan struct{}
	running map[string]*cronRunEntry // 正在运行的 job ID → 运行元数据，防重入
	mu      sync.Mutex
}

// Default 全局默认调度器实例
var Default = &Scheduler{
	stop:    make(chan struct{}),
	running: make(map[string]*cronRunEntry),
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

// CancelJob 向正在运行的指定 job 发送取消信号（context cancel）。
// 返回 true 表示 job 当前正在运行且已发出取消信号；
// 返回 false 表示 job 当前未运行（不在 running map 中）。
// 注意：仅发送信号，goroutine 响应 ctx 后自行退出并从 running map 清除；
// history 记录的终态将由 runJob 写入 "cancelled"。
func (s *Scheduler) CancelJob(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.running[jobID]
	if !ok {
		return false
	}
	entry.cancel()
	return true
}

// RunningJobs 返回当前所有正在执行的 job 快照。
func (s *Scheduler) RunningJobs() []RunningJobInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	result := make([]RunningJobInfo, 0, len(s.running))
	for id, entry := range s.running {
		result = append(result, RunningJobInfo{
			JobID:     id,
			StartedAt: entry.startedAt,
			ElapsedMs: now.Sub(entry.startedAt).Milliseconds(),
		})
	}
	return result
}

// tick 查询所有到期 job，为每个 job 启动独立 goroutine 执行
func (s *Scheduler) tick() {
	// 先清理超时残留条目，防止 goroutine 泄漏导致 job 永久无法触发
	s.reapStale()

	jobs, err := GetDueJobs()
	if err != nil {
		logger.Error("cron", "", "", "get due jobs failed", err, 0)
		return
	}
	for _, job := range jobs {
		s.mu.Lock()
		if s.running[job.ID] != nil {
			s.mu.Unlock()
			continue // 防重入：上一次执行尚未结束
		}
		ctx, cancel := context.WithTimeout(context.Background(), cronJobTimeout)
		s.running[job.ID] = &cronRunEntry{startedAt: time.Now(), cancel: cancel}
		s.mu.Unlock()

		go func(j storage.CronJob, c context.Context, cancelFn context.CancelFunc) {
			defer func() {
				cancelFn()
				s.mu.Lock()
				delete(s.running, j.ID)
				s.mu.Unlock()
			}()
			s.runJob(c, j, "", 0) // 调度器路径：session 和 history 由 runJob 内部创建
		}(job, ctx, cancel)
	}
}

// reapStale 扫描 running 中超过 cronStaleThreshold 仍未退出的条目。
// 正常情况下 context 超时已使 goroutine 退出并触发 deferred delete，此函数是兜底保障：
// 对残留条目调用 cancel() 并强制从 map 中清除，避免 job 永久无法触发。
func (s *Scheduler) reapStale() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, entry := range s.running {
		if time.Since(entry.startedAt) > cronStaleThreshold {
			entry.cancel() // 取消对应 goroutine 的 context（若仍在运行）
			delete(s.running, id)
			logger.Warn("cron", "", "",
				fmt.Sprintf("reapStale: job %q exceeded %v, force-cleared from running map", id, cronStaleThreshold), 0)
		}
	}
}

// runJob 为一个到期的 job 运行 agent，结果写回 creator session。
// ctx 由调用方（tick goroutine 或 RunJobNow）提供，已设置超时，超时后 agent.Run 自动中断。
// preSessionID：非空时直接使用，跳过 session 确定逻辑（由 HTTP handler 预先创建）。
// preHistID：>0 时直接使用，跳过 history 记录创建（由 HTTP handler 同步预创建）。
func (s *Scheduler) runJob(ctx context.Context, job storage.CronJob, preSessionID string, preHistID uint) {
	if agentRunner == nil {
		logger.Error("cron", job.UserID, "", "agentRunner not set, skipping job", fmt.Errorf("agentRunner is nil"), 0)
		return
	}

	// 确定 sessionID：优先用预传值；否则使用 job 的创建 session；再否则创建独立 cron session
	sessionID := preSessionID
	if sessionID == "" {
		sessionID = job.CreatorSessionID
		if sessionID == "" {
			sessionID = uuid.New().String()
			if err := storage.CreateSessionWithSource(sessionID, job.UserID, "cron"); err != nil {
				logger.Error("cron", job.UserID, "", "create fallback session failed", err, 0)
				return
			}
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

	// 写入执行历史（running 状态）：若 HTTP handler 已预创建（preHistID > 0）则直接复用
	var runHistoryID uint
	if preHistID > 0 {
		runHistoryID = preHistID
	} else {
		var histErr error
		runHistoryID, histErr = storage.CreateCronRunHistory(job.ID, job.UserID, job.Name, sessionID)
		if histErr != nil {
			logger.Warn("cron", job.UserID, sessionID,
				fmt.Sprintf("create run history for job %q failed: %v", job.ID, histErr), 0)
		}
	}

	// 运行 agent（即使中途崩溃，next_run_at 已推进 / enabled 已关闭，不会重复触发）
	runErr := agentRunner(ctx, job.UserID, sessionID, job.Name, job.Message)

	// 更新历史记录为终态
	if runHistoryID > 0 {
		finalStatus := "succeeded"
		finalErrMsg := ""
		if runErr != nil {
			if errors.Is(runErr, context.DeadlineExceeded) {
				finalStatus = "timed_out"
				finalErrMsg = fmt.Sprintf("job exceeded max runtime (%v)", cronJobTimeout)
			} else if errors.Is(runErr, context.Canceled) {
				finalStatus = "cancelled"
				finalErrMsg = "job cancelled"
			} else {
				finalStatus = "failed"
				finalErrMsg = runErr.Error()
			}
		}
		if updErr := storage.UpdateCronRunHistory(runHistoryID, finalStatus, finalErrMsg); updErr != nil {
			logger.Warn("cron", job.UserID, sessionID,
				fmt.Sprintf("update run history #%d failed: %v", runHistoryID, updErr), 0)
		}
	}

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

// RunJobNow 立即（在新 goroutine 中）执行一次指定 job，供 cron tool 和 HTTP handler 调用。
// preSessionID / preHistID：由 HTTP handler 同步预创建时传入，让 runJob 跳过重复创建；
// 调度器内部路径（tool/cron）传 "", 0 即可。
// 返回 true 表示 goroutine 已启动；返回 false 表示 job 当前已在运行中，本次调用被忽略。
func (s *Scheduler) RunJobNow(job storage.CronJob, preSessionID string, preHistID uint) bool {
	s.mu.Lock()
	if s.running[job.ID] != nil {
		s.mu.Unlock()
		logger.Warn("cron", job.UserID, "",
			fmt.Sprintf("RunJobNow: job %q already running, skipped", job.ID), 0)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), cronJobTimeout)
	s.running[job.ID] = &cronRunEntry{startedAt: time.Now(), cancel: cancel}
	s.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.running, job.ID)
			s.mu.Unlock()
		}()
		s.runJob(ctx, job, preSessionID, preHistID)
	}()
	return true
}
