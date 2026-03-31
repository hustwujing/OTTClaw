// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/agent/orphan_recovery.go — 服务重启后孤儿子任务自动续跑
//
// 问题：子 agent 在独立 goroutine 中运行。若进程在任务执行期间重启，
// goroutine 消失，但 sub_tasks 表中该记录仍停留在 queued/running 状态。
//
// 解决方案（自动续跑）：
// 服务启动、agent.Init() 完成后，异步扫描所有 queued/running 任务并自动重启：
//   - queued  → 任务从未开始，用原始 prompt 重新派发（完全等价于首次 spawn）
//   - running → 任务执行到一半，注入系统恢复提示，LLM 读取已有对话历史后继续
// RunBackground 内部处理所有后续状态写入和父会话通知，无需额外逻辑。
//
// notifyOrphan（轻量失败通知，不触发 LLM）由 subtask_gc.go 的 goroutine 泄漏
// 检测路径复用，此处不再调用。
package agent

import (
	"encoding/json"
	"fmt"

	"OTTClaw/internal/feishu"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/push"
	"OTTClaw/internal/storage"
)

const orphanErrMsg = "服务重启，任务中断，请重新发起该任务"

// RecoverOrphanSubTasks 扫描所有 queued/running 状态的子任务并自动续跑。
// 应在服务启动完成后在独立 goroutine 中调用，避免阻塞主流程。
func (a *Agent) RecoverOrphanSubTasks() {
	tasks, err := storage.ListOrphanSubTasks()
	if err != nil {
		logger.Warn("subagent", "", "",
			fmt.Sprintf("orphan recovery: list orphans failed: %v", err), 0)
		return
	}
	if len(tasks) == 0 {
		return
	}

	logger.Warn("subagent", "", "",
		fmt.Sprintf("orphan recovery: found %d interrupted sub_task(s), auto-resuming", len(tasks)), 0)

	for _, t := range tasks {
		t := t // capture loop variable for goroutine

		// queued：从未开始，直接用原始 prompt 重新派发。
		// running：执行到一半，注入系统恢复提示，让 LLM 读取已有对话历史后继续。
		var resumeMsg string
		if t.Status == "queued" {
			resumeMsg = t.TaskDesc
		} else {
			resumeMsg = buildResumePrompt(t.TaskDesc)
		}

		logger.Info("subagent", t.UserID, t.ParentSessionID,
			fmt.Sprintf("orphan recovery: resuming task #%d (was %s, child=%s)",
				t.ID, t.Status, t.ChildSessionID), 0)

		a.bgWg.Add(1)
		go func() {
			defer a.bgWg.Done()
			a.RunBackground(t.ID, t.UserID, t.ChildSessionID, resumeMsg, t.ParentSessionID)
		}()
	}

	logger.Info("subagent", "", "",
		fmt.Sprintf("orphan recovery: %d task(s) queued for resume", len(tasks)), 0)
}

// buildResumePrompt 为 running 状态的孤儿任务构造续跑提示。
// 子 agent 会看到已有对话历史 + 此提示，从中断处继续，无需重新开始。
func buildResumePrompt(originalTask string) string {
	return "[系统恢复] 服务重启，上次执行被中断。" +
		"已有的对话历史已恢复——请从中断处继续完成任务，无需重新开始。\n\n" +
		"原始任务：\n\n" + originalTask
}

// RecoverFailedNotifications 扫描 notify_status='failed' 的终态子任务批次，
// 重置 notify_status 并重新触发 maybeNotifyBatch，恢复因进程重启/LLM 瞬时错误导致的丢失通知。
// 应在服务启动后在独立 goroutine 中调用（与 RecoverOrphanSubTasks 并行）。
func (a *Agent) RecoverFailedNotifications() {
	batches, err := storage.ListFailedNotifyBatches()
	if err != nil {
		logger.Warn("subagent", "", "",
			fmt.Sprintf("failed-notify recovery: list batches failed: %v", err), 0)
		return
	}
	if len(batches) == 0 {
		return
	}

	logger.Warn("subagent", "", "",
		fmt.Sprintf("failed-notify recovery: found %d batch(es) to retry", len(batches)), 0)

	for _, b := range batches {
		b := b
		if err := storage.ResetSubTaskNotifyStatusByGroup(b.ParentSessionID, b.ParentTaskID); err != nil {
			logger.Warn("subagent", b.UserID, b.ParentSessionID,
				fmt.Sprintf("failed-notify recovery: reset notify_status failed (parent_task_id=%d): %v",
					b.ParentTaskID, err), 0)
			continue
		}
		logger.Info("subagent", b.UserID, b.ParentSessionID,
			fmt.Sprintf("failed-notify recovery: retrying batch (parent_task_id=%d)", b.ParentTaskID), 0)
		a.bgWg.Add(1)
		go func() {
			defer a.bgWg.Done()
			a.maybeNotifyBatch(b.ParentSessionID, b.UserID, b.ParentTaskID)
		}()
	}
}

// notifyOrphan 向父会话发送轻量失败通知，不触发任何 LLM 调用，并将投递结果写回 DB。
func notifyOrphan(t storage.SubTask) {
	setNotify := func(status, notifyErr string) {
		if dbErr := storage.UpdateSubTaskNotifyStatus(t.ID, status, notifyErr); dbErr != nil {
			logger.Warn("subagent", t.UserID, t.ParentSessionID,
				fmt.Sprintf("orphan recovery: update notify_status for task #%d failed: %v", t.ID, dbErr), 0)
		}
	}

	sess, err := storage.GetSession(t.ParentSessionID)
	if err != nil || sess == nil {
		setNotify("parent_missing", "")
		return
	}

	switch sess.Source {
	case "web":
		// 推送 subagent_error 事件，格式与 SubagentWriter.WriteError 一致，
		// 前端收到后将对应任务卡片标记为失败。
		b, _ := json.Marshal(map[string]any{
			"type":       "subagent_error",
			"task_id":    t.ID,
			"content":    orphanErrMsg,
			"elapsed_ms": 0,
		})
		push.Default.Publish(t.ParentSessionID, b)
		// SSE push 已触发，但父会话不一定在线，标记 session_queued
		setNotify("session_queued", "")

	case "feishu":
		cfg, cfgErr := storage.GetFeishuConfig(sess.UserID)
		if cfgErr != nil || cfg == nil || cfg.AppID == "" {
			setNotify("failed", "feishu config missing")
			return
		}
		taskTitle := fmt.Sprintf("#%d", t.ID)
		if t.Label != "" {
			taskTitle = fmt.Sprintf("#%d「%s」", t.ID, t.Label)
		}
		text := fmt.Sprintf("❌ 子任务 %s 已中断\n服务重启，执行进程已退出，请重新发起该任务。", taskTitle)
		if sendErr := feishu.SendTextTo(cfg.AppID, sess.FeishuPeer, receiveIDTypeFromPeer(sess.FeishuPeer), text); sendErr != nil {
			logger.Warn("subagent", t.UserID, t.ParentSessionID,
				fmt.Sprintf("orphan recovery: feishu notify task #%d failed: %v", t.ID, sendErr), 0)
			setNotify("failed", sendErr.Error())
			return
		}
		// SendTextTo 成功即表示消息已投递到飞书 API
		setNotify("delivered", "")

	default:
		// cron / 其他来源：父会话也已中断，静默处理，不写 notify_status
	}
}
