// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/notify_parent.go — notify_parent 工具：子 agent 向父 agent 注入中间消息
//
// 与 report_task_progress 的区别：
//   - report_task_progress：更新 DB 的 progress_summary 字段 + 可选 SSE 推送给用户前端，
//     父 agent LLM 不会被唤醒。
//   - notify_parent：异步触发父 session 的新一轮 LLM 执行，父 agent 可实时感知并响应。
//
// 调用后立即返回，父 session 的 LLM run 在独立 goroutine 中执行，不阻塞子任务。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

type notifyParentArgs struct {
	Message string `json:"message"`
}

func handleNotifyParent(ctx context.Context, argsJSON string) (string, error) {
	var args notifyParentArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("notify_parent: invalid args: %w", err)
	}
	if args.Message == "" {
		return "", fmt.Errorf("notify_parent: message is required")
	}

	// 仅限子 agent 上下文（RunBackground 中注入了 taskID）
	taskID := taskIDFromCtx(ctx)
	if taskID == 0 {
		return "", fmt.Errorf("notify_parent: not available outside subagent context")
	}

	notifier := parentNotifierFromCtx(ctx)
	if notifier == nil {
		return "", fmt.Errorf("notify_parent: not available in this context")
	}

	notifier(args.Message)

	b, _ := json.Marshal(map[string]any{
		"task_id": taskID,
		"note":    "消息已异步发送给父 agent，父 agent 将在新一轮 LLM turn 中处理。当前子任务继续执行，无需等待父 agent 响应。",
	})
	return string(b), nil
}
