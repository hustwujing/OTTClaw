// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/agent/session_scorer.go — Langfuse Task Unit 评估主逻辑
//
// 评估流程：
//  1. DB 原子抢占锁（防止条件 A / 条件 B 并发触发同一 session）
//  2. 取游标之后的 origin 消息
//  3. 调 LLM 判断是否有已结案的任务（完成或放弃均算）
//  4. 若有：上报 score 到 Langfuse，推进游标；否则解锁等待下次触发
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"OTTClaw/config"
	"OTTClaw/internal/langfuse"
	"OTTClaw/internal/llm"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

// scorerLLMPrompt 是发给 LLM 的系统提示，用于识别任务边界和评估完成度
const scorerLLMPrompt = `你是一个对话任务评估器。给定一段对话记录（每条消息含 id/role/content），
请找出其中第一个已经结案的任务（明确完成或明确放弃均算结案），返回以下 JSON：

{
  "task_complete": true/false,       // 是否包含一个可结案的任务
  "task_name": "...",                // 任务一句话描述（task_complete=true 时必填）
  "end_msg_id": <uint or null>,      // 任务结束的消息 ID（task_complete=false 时返回 null）
  "scores": {
    "completion": 0.0-1.0,           // 用户是否得到了想要的结果（1=完全满意，0=完全未达到）
    "efficiency": 0.0-1.0,           // 完成效率（交互轮次少、无工具报错=高分）
    "user_signal": "satisfied"       // "satisfied" | "abandoned" | "redirected"
  },
  "reason": "..."                    // 判断依据（写入 score comment，50字以内）
}

注意：
- 仅返回纯 JSON，不要添加 markdown 代码块或其他文字
- 如果对话中还没有任何任务结案，返回 {"task_complete": false, "end_msg_id": null}
- end_msg_id 必须是消息列表中实际存在的 id`

// scorerResult 是 LLM 返回的评估结果
type scorerResult struct {
	TaskComplete bool   `json:"task_complete"`
	TaskName     string `json:"task_name"`
	EndMsgID     *uint  `json:"end_msg_id"`
	Scores       struct {
		Completion float64 `json:"completion"`
		Efficiency float64 `json:"efficiency"`
		UserSignal string  `json:"user_signal"`
	} `json:"scores"`
	Reason string `json:"reason"`
}

// signalToFloat 将用户信号转为 0-1 分值
func signalToFloat(signal string) float64 {
	switch signal {
	case "satisfied":
		return 1.0
	case "redirected":
		return 0.5
	default: // "abandoned"
		return 0.0
	}
}

// MaybeScoreSession 条件 B 入口：统计游标之后的消息数，达到阈值时触发评估。
// 在 sse.go / ws.go 的 Run() 返回后异步调用，不阻塞响应。
func (a *Agent) MaybeScoreSession(sessionID, userID string) {
	if !config.Cfg.LangfuseEnabled || a.langfuseClient == nil {
		return
	}
	maxWindow := config.Cfg.LangfuseScorerMaxWindow
	if maxWindow <= 0 {
		return
	}
	count, err := storage.CountOriginMsgsAfterCursor(sessionID)
	if err != nil || count < int64(maxWindow) {
		return
	}
	logger.Info("scorer", userID, sessionID,
		fmt.Sprintf("[scorer] condition-B triggered: %d msgs >= window(%d)", count, maxWindow), 0)
	a.ScoreSession(sessionID, userID)
}

// ScoreSession 对指定 session 进行 Task Unit 评估。
// 线程安全：通过 DB 原子锁防止并发重复处理同一 session。
// 仅在 Langfuse 已启用时有实际效果。
func (a *Agent) ScoreSession(sessionID, userID string) {
	if !config.Cfg.LangfuseEnabled || a.langfuseClient == nil {
		return
	}

	logger.Info("scorer", userID, sessionID, "[scorer] ScoreSession started", 0)

	// 1. 原子抢占锁
	now := time.Now()
	if !storage.TryLockScoringSession(sessionID, now) {
		logger.Info("scorer", userID, sessionID, "[scorer] lock busy, another goroutine is scoring, skip", 0)
		return
	}
	// 确保无论如何都解锁（UpdateLangfuseCursor 成功时会顺带清锁，失败时 defer 保底）
	locked := true
	defer func() {
		if locked {
			storage.UnlockScoringSession(sessionID)
		}
	}()

	// 2. 取游标及游标之后的 origin 消息
	cursorID, err := storage.GetSessionCursorMsgID(sessionID)
	if err != nil {
		logger.Error("scorer", userID, sessionID, "get cursor failed", err, 0)
		return
	}

	msgs, err := storage.GetOriginMsgsAfterCursor(sessionID, cursorID)
	if err != nil {
		logger.Error("scorer", userID, sessionID, "get origin msgs failed", err, 0)
		return
	}
	if len(msgs) == 0 {
		return
	}
	logger.Info("scorer", userID, sessionID,
		fmt.Sprintf("[scorer] evaluating %d msgs after cursor=%d", len(msgs), cursorID), 0)

	// 3. 构造 LLM 输入（id + role + content）
	type msgItem struct {
		ID      uint   `json:"id"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	items := make([]msgItem, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		if len([]rune(content)) > 500 {
			runes := []rune(content)
			content = string(runes[:500]) + "…"
		}
		items = append(items, msgItem{ID: m.ID, Role: m.Role, Content: content})
	}
	inputJSON, _ := json.Marshal(items)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	llmMsgs := []llm.ChatMessage{
		{Role: "system", Content: scorerLLMPrompt},
		{Role: "user", Content: string(inputJSON)},
	}
	rawResp, err := a.llmClient.ChatSync(ctx, llmMsgs)
	if err != nil {
		logger.Error("scorer", userID, sessionID, "llm call failed", err, 0)
		return
	}
	logger.Info("scorer", userID, sessionID,
		fmt.Sprintf("[scorer] llm raw response: %s", rawResp), 0)

	// 4. 解析 LLM 返回
	rawResp = strings.TrimSpace(rawResp)
	// 去掉可能出现的 markdown 代码块
	if strings.HasPrefix(rawResp, "```") {
		lines := strings.SplitN(rawResp, "\n", 2)
		if len(lines) == 2 {
			rawResp = strings.TrimSuffix(strings.TrimSpace(lines[1]), "```")
		}
	}

	var result scorerResult
	if err := json.Unmarshal([]byte(rawResp), &result); err != nil {
		logger.Error("scorer", userID, sessionID,
			fmt.Sprintf("parse scorer result failed: %s", rawResp), err, 0)
		return
	}

	if !result.TaskComplete || result.EndMsgID == nil {
		// LLM 认为尚无结案任务，不推进游标，等待下次触发
		logger.Info("scorer", userID, sessionID, "no completed task found, skip cursor advance", 0)
		return
	}

	// 5. 为本次 Task Unit 评估创建独立的 eval trace，关联到当前 session。
	// 不复用 agent run 的 traceId，避免 score 语义混淆（任务可跨多个 agent run）。
	evalTraceID := uuid.New().String()
	t := time.Now()
	a.langfuseClient.Enqueue(langfuse.Event{
		ID:        uuid.New().String(),
		Timestamp: t,
		Type:      "trace-create",
		Body: langfuse.TraceBody{
			ID:        evalTraceID,
			Name:      "task-unit-eval",
			UserID:    userID,
			SessionID: sessionID,
			Input:     fmt.Sprintf("cursor_msg_id=%d msgs=%d", cursorID, len(msgs)),
			Output:    result.TaskName,
			Metadata: map[string]any{
				"task_name":   result.TaskName,
				"end_msg_id":  result.EndMsgID,
				"reason":      result.Reason,
				"user_signal": result.Scores.UserSignal,
			},
			Tags: []string{"eval", "task-unit"},
		},
	})

	// 6. 上报 3 个 score，挂到 eval trace 上。
	// ScoreBody.ID 与外层 Event.ID 保持一致，供 Langfuse 端去重和关联使用。
	comment := fmt.Sprintf("[%s] %s", result.TaskName, result.Reason)
	id1, id2, id3 := uuid.New().String(), uuid.New().String(), uuid.New().String()
	a.langfuseClient.Enqueue(
		langfuse.Event{
			ID:        id1,
			Timestamp: t,
			Type:      "score-create",
			Body: langfuse.ScoreBody{
				ID:       id1,
				TraceID:  evalTraceID,
				Name:     "task_completion",
				Value:    result.Scores.Completion,
				DataType: "NUMERIC",
				Comment:  comment,
			},
		},
		langfuse.Event{
			ID:        id2,
			Timestamp: t,
			Type:      "score-create",
			Body: langfuse.ScoreBody{
				ID:       id2,
				TraceID:  evalTraceID,
				Name:     "task_efficiency",
				Value:    result.Scores.Efficiency,
				DataType: "NUMERIC",
			},
		},
		langfuse.Event{
			ID:        id3,
			Timestamp: t,
			Type:      "score-create",
			Body: langfuse.ScoreBody{
				ID:       id3,
				TraceID:  evalTraceID,
				Name:     "user_signal",
				Value:    result.Scores.UserSignal,
				DataType: "CATEGORICAL",
			},
		},
	)

	logger.Info("scorer", userID, sessionID,
		fmt.Sprintf("[scorer] enqueued eval trace + 3 scores | evalTraceId=%s task=%q end_msg=%d | completion=%.2f efficiency=%.2f user_signal=%s(%s)",
			evalTraceID, result.TaskName, *result.EndMsgID,
			result.Scores.Completion, result.Scores.Efficiency,
			result.Scores.UserSignal, formatSignal(result.Scores.UserSignal),
		), 0)

	// 7. 推进游标（同时清锁）
	if err := storage.UpdateLangfuseCursor(sessionID, *result.EndMsgID); err != nil {
		logger.Error("scorer", userID, sessionID, "update cursor failed", err, 0)
		return
	}
	locked = false // UpdateLangfuseCursor 已顺带清锁，defer 无需再次解锁

	logger.Info("scorer", userID, sessionID,
		fmt.Sprintf("[scorer] done, cursor advanced to msg=%d", *result.EndMsgID), 0)
}

// formatSignal 把 user_signal 转成带数值的可读字符串，方便日志对照
func formatSignal(signal string) string {
	return fmt.Sprintf("%.1f", signalToFloat(signal))
}
