// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/agent/subtask_gc.go — 子任务定期 sweep：goroutine 泄漏检测 + GC
//
// 问题一：24 小时 GC 间隔太长，终态任务积压；
// 问题二：孤儿恢复只在启动时执行一次，当前进程中的 goroutine 泄漏无法被发现。
//
// 解决方案：用 1 分钟间隔的统一 sweep 替代原来的 24 小时 GC ticker：
//   - stale 检测：queued/running 任务若超过 subTaskStaleThreshold（35 min）
//     仍未进入终态，视为 goroutine 泄漏或静默 panic，标记为 lost 并通知父会话。
//   - GC：删除 updated_at 早于保留期（默认 7 天）的终态任务。
//
// 与 RecoverOrphanSubTasks 的分工：
//   - RecoverOrphanSubTasks（main.go 启动时调用）：标记上次进程退出遗留的所有孤儿，
//     无时间过滤，处理"历史崩溃"场景。
//   - startSubTaskSweep（本文件，每分钟运行）：处理当前进程中因 goroutine 泄漏
//     导致的超时任务，时间过滤避免误判正常运行中的任务。
package agent

import (
	"fmt"
	"time"

	"OTTClaw/config"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

const (
	subTaskSweepInterval = 1 * time.Minute
	// stale 阈值 = 子 agent 最大超时 + 5 min 宽限期。
	// 超过此时间仍处于 queued/running 的任务视为 goroutine 泄漏。
	subTaskStaleThreshold = subagentTimeout + 5*time.Minute // 35 min
)

// startSubTaskSweep 启动统一 sweep 后台 goroutine，每分钟执行一次。
// 立即执行首轮（清理启动期间积累的过期记录），随后每 subTaskSweepInterval 执行一次。
// 在 agent.Init() 后以 go 调用，监听 bgCtx 取消（Shutdown 时自动退出）。
func (a *Agent) startSubTaskSweep() {
	days := config.Cfg.SubTaskRetentionDays

	// 启动时立即执行一次
	a.runSweep(days)

	ticker := time.NewTicker(subTaskSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.runSweep(days)
		case <-a.bgCtx.Done():
			return
		}
	}
}

// runSweep 执行单次 sweep：stale 检测 + GC。
func (a *Agent) runSweep(retentionDays int) {
	a.detectStaleTasks()
	if retentionDays > 0 {
		retention := time.Duration(retentionDays) * 24 * time.Hour
		a.gcTerminalTasks(retention)
		a.gcCronRunHistory(retention)
	}
}

// detectStaleTasks 检测当前进程中因 goroutine 泄漏或静默 panic 导致的超时任务，
// 将其标记为 lost 并通知父会话（复用 notifyOrphan 轻量通知路径）。
func (a *Agent) detectStaleTasks() {
	tasks, err := storage.ListStaleActiveTasks(subTaskStaleThreshold)
	if err != nil {
		logger.Warn("subagent-sweep", "", "",
			fmt.Sprintf("sweep: list stale tasks failed: %v", err), 0)
		return
	}
	for _, t := range tasks {
		if err := storage.UpdateSubTaskStatus(t.ID, "lost", "", "goroutine leak: task exceeded max runtime without completing"); err != nil {
			logger.Warn("subagent-sweep", t.UserID, t.ParentSessionID,
				fmt.Sprintf("sweep: mark task #%d lost failed: %v", t.ID, err), 0)
			continue
		}
		logger.Warn("subagent-sweep", t.UserID, t.ParentSessionID,
			fmt.Sprintf("sweep: task #%d (child=%s) marked as lost (stale >%v)", t.ID, t.ChildSessionID, subTaskStaleThreshold), 0)
		notifyOrphan(t)
	}
}

// gcCronRunHistory 删除 started_at 早于 retention 的 cron 执行历史记录。
func (a *Agent) gcCronRunHistory(retention time.Duration) {
	before := time.Now().Add(-retention)
	n, err := storage.DeleteExpiredCronRunHistory(before)
	if err != nil {
		logger.Warn("cron-gc", "", "",
			fmt.Sprintf("gc: delete expired cron run history failed: %v", err), 0)
		return
	}
	if n > 0 {
		logger.Info("cron-gc", "", "",
			fmt.Sprintf("gc: deleted %d expired cron run record(s) (retention=%dd)", n, int(retention.Hours()/24)), 0)
	}
}

// gcTerminalTasks 删除 updated_at 早于 retention 的终态任务。
func (a *Agent) gcTerminalTasks(retention time.Duration) {
	before := time.Now().Add(-retention)
	n, err := storage.DeleteExpiredSubTasks(before)
	if err != nil {
		logger.Warn("subagent-gc", "", "",
			fmt.Sprintf("gc: delete expired tasks failed: %v", err), 0)
		return
	}
	if n > 0 {
		logger.Info("subagent-gc", "", "",
			fmt.Sprintf("gc: deleted %d expired task(s) (retention=%dd)", n, int(retention.Hours()/24)), 0)
	}
}
