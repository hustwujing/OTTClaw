// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/agent/titler.go — 会话 AI 标题生成
//
// 触发条件：会话恰好完成第 3 轮对话（user 消息数 == 3）且尚无 AI 标题。
// 在后台 goroutine 中异步执行，不阻塞主对话流程。
// 标题长度上限 20 个字符，超出截断。
package agent

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"OTTClaw/internal/llm"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

const titleTriggerRound = 3 // 第几轮后生成标题

// maybeGenerateTitle 检查是否需要为会话生成 AI 标题，满足条件则异步生成。
// 使用独立的 context（带 30s 超时），避免请求 context 取消后中断标题生成。
func (a *Agent) maybeGenerateTitle(sessionID string) {
	// 检查是否已有标题
	sess, err := storage.GetSession(sessionID)
	if err != nil || sess == nil || sess.Title != "" {
		return
	}

	// 仅当 user 消息数恰好等于触发轮数时执行（确保只生成一次）
	count, err := storage.CountUserMessages(sessionID)
	if err != nil || count != int64(titleTriggerRound) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 获取对话记录
	msgs, err := storage.GetDisplayMessages(sessionID)
	if err != nil || len(msgs) == 0 {
		return
	}

	// 构造对话摘要文本（每条消息截取前 120 字，避免 prompt 过长）
	var sb strings.Builder
	for _, m := range msgs {
		role := "用户"
		if m.Role == "assistant" {
			role = "AI"
		}
		content := m.Content
		if utf8.RuneCountInString(content) > 120 {
			runes := []rune(content)
			content = string(runes[:120]) + "…"
		}
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(content)
		sb.WriteString("\n")
	}

	req := []llm.ChatMessage{
		{
			Role: "user",
			Content: "请为以下对话生成一个简洁的中文标题，不超过15个字，" +
				"只输出标题本身，不要引号、序号或任何其他文字：\n\n" + sb.String(),
		},
	}

	title, err := a.llmClient.ChatSync(ctx, req)
	if err != nil {
		logger.Warn("agent", "", sessionID, "generate session title failed: "+err.Error(), 0)
		return
	}

	// 清理 LLM 可能附带的引号、空白
	title = strings.TrimSpace(title)
	title = strings.Trim(title, `"'""''`)
	title = strings.TrimSpace(title)

	if title == "" {
		return
	}

	// 截断超长标题
	runes := []rune(title)
	if len(runes) > 20 {
		title = string(runes[:20])
	}

	if err := storage.UpdateSessionTitle(sessionID, title); err != nil {
		logger.Warn("agent", "", sessionID, "save session title failed: "+err.Error(), 0)
		return
	}
	logger.Info("agent", "", sessionID, "session title generated: "+title, 0)
}
