// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/spawn_subagent.go — 子 agent 派发与查询工具
//
// spawn_subagent：将任务委托给独立的子 agent 异步执行，立即返回 task_id。
// get_subtask_result：查询子 agent 任务当前状态和结果。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"OTTClaw/internal/storage"
)

// ---- spawn_subagent ----

type spawnSubagentArgs struct {
	Task         string `json:"task"`          // 子任务描述（必填）
	Label        string `json:"label"`         // 简短可读任务标签（可选），如"搜索竞品信息"
	Context      string `json:"context"`       // 背景信息（可选，拼入发给子 agent 的 prompt）
	NotifyPolicy string `json:"notify_policy"` // 通知策略（可选）：done_only | state_changes | silent，默认 done_only
	RetainHours  int    `json:"retain_hours"`  // 任务终态后保留小时数（可选）；0 = 使用全局保留窗口
}

var validNotifyPolicies = map[string]struct{}{
	"done_only":     {},
	"state_changes": {},
	"silent":        {},
}

func handleSpawnSubagent(ctx context.Context, argsJSON string) (string, error) {
	var args spawnSubagentArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("spawn_subagent: invalid args: %w", err)
	}
	if args.Task == "" {
		return "", fmt.Errorf("spawn_subagent: task is required")
	}
	if args.NotifyPolicy != "" {
		if _, ok := validNotifyPolicies[args.NotifyPolicy]; !ok {
			return "", fmt.Errorf("spawn_subagent: invalid notify_policy %q, must be done_only | state_changes | silent", args.NotifyPolicy)
		}
	}

	spawner := subagentSpawnerFromCtx(ctx)
	if spawner == nil {
		return "", fmt.Errorf("spawn_subagent: not available in this context")
	}

	userID := userIDFromCtx(ctx)
	parentSessionID := sessionIDFromCtx(ctx)

	// 读取父会话，用于派生 notify_policy 和 runtime
	var parentSource string
	if sess, err := storage.GetSession(parentSessionID); err == nil && sess != nil {
		parentSource = sess.Source
	}

	// runtime 继承父会话来源（cron/feishu 透传，其他均为 subagent）
	// 注意：cron 来源不再自动降级为 silent——notifyParent 对 cron 父会话使用
	// resultWriter 静默触发 followup LLM 轮次，实现链式子 agent 派发（followup 机制）。
	runtime := "subagent"
	if parentSource == "cron" || parentSource == "feishu" {
		runtime = parentSource
	}

	// 构造子 agent 的初始 prompt
	taskDesc := buildSubagentPrompt(args.Task, args.Context)

	// 创建子会话（source=subagent，不出现在用户侧栏）
	childSessionID := uuid.New().String()
	if err := storage.CreateSessionWithSource(childSessionID, userID, "subagent"); err != nil {
		return "", fmt.Errorf("spawn_subagent: create child session: %w", err)
	}
	// 标记子会话元数据
	if err := storage.DB.Model(&storage.Session{}).
		Where("session_id = ?", childSessionID).
		Updates(map[string]any{
			"is_subagent":       true,
			"subagent_task":     args.Task,
			"parent_session_id": parentSessionID,
		}).Error; err != nil {
		return "", fmt.Errorf("spawn_subagent: update child session meta: %w", err)
	}

	// 若调用方自身也是子 agent，记录任务树父子关系
	parentTaskID := taskIDFromCtx(ctx) // 顶层调用返回 0，嵌套子 agent 调用返回父任务 ID

	// 写入 sub_tasks 记录（初始 queued）
	task, err := storage.CreateSubTask(userID, parentSessionID, childSessionID, taskDesc, args.NotifyPolicy, args.Label, runtime, parentTaskID, args.RetainHours)
	if err != nil {
		return "", fmt.Errorf("spawn_subagent: create sub_task: %w", err)
	}

	// 异步启动后台 agent
	spawner(task.ID, userID, childSessionID, taskDesc, parentSessionID)

	out := map[string]any{
		"task_id":          task.ID,
		"child_session_id": childSessionID,
		"status":           "queued",
		"note":             "子 agent 已派发，正在后台执行。完成后系统将自动把结果注入本会话——无需轮询。继续处理其他工作或等待完成通知即可。",
	}
	if task.Label != "" {
		out["label"] = task.Label
	}
	if task.ParentTaskID != 0 {
		out["parent_task_id"] = task.ParentTaskID
	}
	result, _ := json.Marshal(out)
	return string(result), nil
}

// buildSubagentPrompt 构造传给子 agent 的完整初始 prompt
func buildSubagentPrompt(task, background string) string {
	prompt := "[Subagent Task]\n" +
		"你是一个专注执行单一任务的 AI Agent。\n" +
		"父 Agent 分配给你的工作是：\n\n" +
		task
	if background != "" {
		prompt += "\n\n背景信息：\n" + background
	}
	prompt += "\n\n请专注完成上述任务并给出结果。执行过程中，每完成一个重要阶段性步骤时，调用 report_task_progress 工具上报当前进度（如：已完成数据收集，正在分析…），让用户实时了解执行状态。完成后直接输出结果，无需额外解释。"
	return prompt
}

// ---- cancel_subtask ----

type cancelSubtaskArgs struct {
	TaskID uint   `json:"task_id"`
	Reason string `json:"reason"` // 可选取消原因
	Force  bool   `json:"force"`  // true = 强制立即置为 killed（不等待 goroutine 响应 context.Canceled）
}

func handleCancelSubtask(ctx context.Context, argsJSON string) (string, error) {
	var args cancelSubtaskArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("cancel_subtask: invalid args: %w", err)
	}
	if args.TaskID == 0 {
		return "", fmt.Errorf("cancel_subtask: task_id is required")
	}

	// 先查 DB 确认任务存在且处于可取消状态
	task, err := storage.GetSubTask(args.TaskID)
	if err != nil {
		return "", fmt.Errorf("cancel_subtask: db error: %w", err)
	}
	if task == nil {
		return "", fmt.Errorf("cancel_subtask: task %d not found", args.TaskID)
	}
	switch task.Status {
	case "succeeded", "failed", "timed_out", "lost", "cancelled", "killed":
		b, _ := json.Marshal(map[string]any{
			"task_id": task.ID,
			"status":  task.Status,
			"note":    fmt.Sprintf("任务已处于终态 %q，无需取消", task.Status),
		})
		return string(b), nil
	}

	canceler := subtaskCancelerFromCtx(ctx)
	if canceler == nil {
		return "", fmt.Errorf("cancel_subtask: not available in this context")
	}

	reason := args.Reason
	if reason == "" {
		if args.Force {
			reason = "任务已被强制终止"
		} else {
			reason = "任务已被主动取消"
		}
	}

	found := canceler(args.TaskID)
	if !found {
		// goroutine 可能刚好已经结束（竞态），查一次 DB 确认最终状态
		updated, _ := storage.GetSubTask(args.TaskID)
		status := "unknown"
		if updated != nil {
			status = updated.Status
		}
		b, _ := json.Marshal(map[string]any{
			"task_id": args.TaskID,
			"status":  status,
			"note":    "任务当前不在运行中（可能已完成或刚刚结束），取消请求无需处理。",
		})
		return string(b), nil
	}

	if args.Force {
		// 强制 kill：立即写入 DB，不等待 goroutine 响应 context.Canceled。
		// RunBackground 检测到 context.Canceled 时会跳过覆盖已有终态。
		_ = storage.ForceKillSubTask(args.TaskID, reason)
		b, _ := json.Marshal(map[string]any{
			"task_id": args.TaskID,
			"status":  "killed",
			"reason":  reason,
			"note":    "任务已被强制终止，DB 状态已立即更新为 killed。",
		})
		return string(b), nil
	}

	// cancel() 已调用；RunBackground goroutine 会检测到 context.Canceled 并将状态写为 cancelled。
	// 此处仅返回确认，由 RunBackground 负责最终 DB 写入，避免并发写冲突。
	b, _ := json.Marshal(map[string]any{
		"task_id": args.TaskID,
		"status":  "cancelling",
		"reason":  reason,
		"note":    "取消信号已发送，任务将在当前 LLM/工具调用完成后停止并标记为 cancelled。",
	})
	return string(b), nil
}

// ---- report_task_progress ----

type reportTaskProgressArgs struct {
	Progress string `json:"progress"` // 进度描述（必填）
}

func handleReportTaskProgress(ctx context.Context, argsJSON string) (string, error) {
	var args reportTaskProgressArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("report_task_progress: invalid args: %w", err)
	}
	args.Progress = strings.TrimSpace(args.Progress)
	if args.Progress == "" {
		return "", fmt.Errorf("report_task_progress: progress is required")
	}

	taskID := taskIDFromCtx(ctx)
	if taskID == 0 {
		return "", fmt.Errorf("report_task_progress: not available outside subagent context")
	}

	reporter := subtaskProgressReporterFromCtx(ctx)
	if reporter == nil {
		return "", fmt.Errorf("report_task_progress: not available in this context")
	}

	reporter(args.Progress)

	b, _ := json.Marshal(map[string]any{
		"task_id":  taskID,
		"progress": args.Progress,
		"note":     "进度已记录。",
	})
	return string(b), nil
}
