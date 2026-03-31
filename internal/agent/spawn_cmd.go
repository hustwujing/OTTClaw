// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/agent/spawn_cmd.go — 用户命令 /subagents spawn 的直接派发实现
//
// 用户可在 webcli 或飞书对话中输入：
//   /subagents spawn <task description>
// 不经过 LLM 决策，直接创建子 agent 并立即返回确认。
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"OTTClaw/internal/storage"
)

// ParseSpawnCmd 解析 /subagents spawn <task> 命令。
// 返回任务描述（已 trim）和 ok=true；不匹配时返回 "", false。
func ParseSpawnCmd(message string) (task string, ok bool) {
	const prefix = "/subagents spawn "
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}
	task = strings.TrimSpace(strings.TrimPrefix(message, prefix))
	if task == "" {
		return "", false
	}
	return task, true
}

// SpawnSubagentCmd 用户命令派发入口：直接创建子 agent，不经过 LLM 决策。
// 内部逻辑与 tool/spawn_subagent.go 中的 handleSpawnSubagent 保持一致：
//   - 从父会话推导 notify_policy 和 runtime
//   - 创建子会话记录 + sub_tasks 记录
//   - 异步启动 RunBackground goroutine
//
// 返回 taskID 和 childSessionID，供调用方生成确认响应。
func (a *Agent) SpawnSubagentCmd(ctx context.Context, userID, parentSessionID, task string) (taskID uint, childSessionID string, err error) {
	// 推导 notify_policy 和 runtime
	// cron 来源不降级为 silent，followup 机制依赖 notifyParent 静默触发新一轮 LLM。
	notifyPolicy := "done_only"
	runtime := "subagent"
	if sess, e := storage.GetSession(parentSessionID); e == nil && sess != nil {
		if sess.Source == "cron" || sess.Source == "feishu" {
			runtime = sess.Source
		}
	}

	// 构造子 agent 的完整初始 prompt（与 buildSubagentPrompt 逻辑相同）
	taskDesc := "[Subagent Task]\n" +
		"你是一个专注执行单一任务的 AI Agent。\n" +
		"父 Agent 分配给你的工作是：\n\n" +
		task +
		"\n\n请专注完成上述任务并给出结果。执行过程中，每完成一个重要阶段性步骤时，调用 report_task_progress 工具上报当前进度（如：已完成数据收集，正在分析…），让用户实时了解执行状态。完成后直接输出结果，无需额外解释。"

	// 创建子会话
	childSessionID = uuid.New().String()
	if err = storage.CreateSessionWithSource(childSessionID, userID, "subagent"); err != nil {
		return 0, "", fmt.Errorf("create child session: %w", err)
	}
	if err = storage.DB.Model(&storage.Session{}).
		Where("session_id = ?", childSessionID).
		Updates(map[string]any{
			"is_subagent":       true,
			"subagent_task":     task,
			"parent_session_id": parentSessionID,
		}).Error; err != nil {
		return 0, "", fmt.Errorf("update child session meta: %w", err)
	}

	// 写入 sub_tasks 记录（初始 queued）
	t, err := storage.CreateSubTask(userID, parentSessionID, childSessionID, taskDesc, notifyPolicy, "", runtime, 0, 0)
	if err != nil {
		return 0, "", fmt.Errorf("create sub_task: %w", err)
	}

	// 异步启动后台 agent（与 tool/spawn_subagent.go 中 spawner 调用方式相同）
	a.bgWg.Add(1)
	go func() {
		defer a.bgWg.Done()
		a.RunBackground(t.ID, userID, childSessionID, taskDesc, parentSessionID)
	}()

	return t.ID, childSessionID, nil
}

// SpawnCmdText 生成 /subagents spawn 命令的确认响应文本，供各端 handler 写入回复。
func SpawnCmdText(task string, taskID uint) string {
	return fmt.Sprintf(
		"子 agent 已派发 ✓\n\n**任务**：%s\n**task_id**：%d\n\n任务正在后台执行，可向 AI 询问「查询子任务 %d 的进度」来获取结果。",
		task, taskID, taskID,
	)
}
