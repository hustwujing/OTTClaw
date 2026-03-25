// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/session_search.go — 跨会话全文搜索工具
// 利用 SQLite FTS5 (或 MySQL LIKE) 在历史会话中检索相关内容，
// 并通过辅助 LLM 调用生成每个命中 session 的摘要。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"OTTClaw/config"
	"OTTClaw/internal/llm"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

// ========== Context 注入：LLM Client ==========

type llmClientCtxKey struct{}

// WithLLMClient 将 LLM 客户端注入 context，供 session_search 等工具调用辅助 LLM
func WithLLMClient(ctx context.Context, c llm.Client) context.Context {
	return context.WithValue(ctx, llmClientCtxKey{}, c)
}

func llmClientFromCtx(ctx context.Context) llm.Client {
	c, _ := ctx.Value(llmClientCtxKey{}).(llm.Client)
	return c
}

// ========== session_search 工具实现 ==========

// sessionSummaryResult session 搜索结果条目
type sessionSummaryResult struct {
	SessionID string `json:"session_id"`
	When      string `json:"when"`
	Summary   string `json:"summary"`
}

const summaryPromptTmpl = `Query: "%s". Summarize the relevant parts of this conversation in 2-3 sentences. Output only the summary.`

// handleSessionSearch 在历史会话中全文搜索，返回命中 session 的摘要列表
func handleSessionSearch(ctx context.Context, argsJSON string) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}
	currentSID := sessionIDFromCtx(ctx)

	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse session_search args: %w", err)
	}
	logger.Debug("tool", userID, currentSID, fmt.Sprintf("[session-search] query=%q limit=%d", args.Query, args.Limit), 0)

	// 无查询词模式：直接返回近期会话元数据，零 LLM 调用
	if args.Query == "" {
		if args.Limit <= 0 {
			args.Limit = 5
		}
		previews, err := storage.GetRecentSessionMeta(userID, currentSID, args.Limit)
		if err != nil {
			return "", fmt.Errorf("get recent sessions: %w", err)
		}
		logger.Debug("tool", userID, currentSID, fmt.Sprintf("[session-search] recent sessions: found %d", len(previews)), 0)
		type recentItem struct {
			SessionID string `json:"session_id"`
			When      string `json:"when"`
			Title     string `json:"title,omitempty"`
			Preview   string `json:"preview,omitempty"`
		}
		items := make([]recentItem, len(previews))
		for i, p := range previews {
			items[i] = recentItem{
				SessionID: p.SessionID,
				When:      p.UpdatedAt.UTC().Format(time.RFC3339),
				Title:     p.Title,
				Preview:   p.Preview,
			}
		}
		b, _ := json.Marshal(items)
		return string(b), nil
	}

	if args.Limit <= 0 {
		args.Limit = 3
	}
	if args.Limit > 5 {
		args.Limit = 5
	}

	// 排除当前 session：其内容已在 context window 中，搜出来是重复信息。
	// 不排除父 session：显式续话时父 session 内容不在当前 context，
	// session_search 存在的意义就是召回这些历史，不应屏蔽。
	excludeIDs := []string{}
	if currentSID != "" {
		excludeIDs = []string{currentSID}
	}

	// 全文搜索命中的 session
	groups, err := storage.SearchSessionMessages(userID, args.Query, excludeIDs, args.Limit)
	if err != nil {
		return "", fmt.Errorf("search sessions: %w", err)
	}
	if len(groups) == 0 {
		logger.Debug("tool", userID, currentSID, fmt.Sprintf("[session-search] no FTS hits for query=%q", args.Query), 0)
		b, _ := json.Marshal([]sessionSummaryResult{})
		return string(b), nil
	}
	totalHits := 0
	for _, g := range groups {
		totalHits += len(g.Hits)
	}
	logger.Debug("tool", userID, currentSID, fmt.Sprintf("[session-search] FTS hits: %d sessions, %d messages", len(groups), totalHits), 0)

	llmClient := llmClientFromCtx(ctx)

	// 2. 并行为每个 session 生成摘要
	results := make([]sessionSummaryResult, len(groups))
	var wg sync.WaitGroup
	for i, g := range groups {
		wg.Add(1)
		go func(idx int, group storage.SessionHitGroup) {
			defer wg.Done()
			logger.Debug("tool", userID, group.SessionID, fmt.Sprintf("[session-search] summarizing session hits=%d", len(group.Hits)), 0)
			when := ""
			if !group.LastHitAt.IsZero() {
				when = group.LastHitAt.UTC().Format(time.RFC3339)
			}
			summary := summarizeSessionHits(ctx, llmClient, group, args.Query)
			logger.Debug("tool", userID, group.SessionID, fmt.Sprintf("[session-search] ✓ summary_len=%d", len(summary)), 0)
			results[idx] = sessionSummaryResult{
				SessionID: group.SessionID,
				When:      when,
				Summary:   summary,
			}
		}(i, g)
	}
	wg.Wait()

	logger.Debug("tool", userID, currentSID, fmt.Sprintf("[session-search] done: returning %d session summaries", len(results)), 0)
	b, _ := json.Marshal(results)
	return string(b), nil
}

// summarizeSessionHits 读取命中 session 的完整消息，调用辅助 LLM 生成摘要。
// 若 LLM 不可用，直接拼接命中片段作为摘要。
// 上下文采用居中截断：将最大字符窗口对齐到关键词命中位置，
// 而非固定取末尾 N 条——避免长对话中关键词不在末尾时摘要质量下降。
func summarizeSessionHits(ctx context.Context, llmClient llm.Client, group storage.SessionHitGroup, query string) string {
	// 优先用命中消息片段拼接上下文
	var snippets []string
	for _, h := range group.Hits {
		if c := strings.TrimSpace(h.Content); c != "" {
			snippets = append(snippets, c)
		}
	}

	if llmClient == nil || len(snippets) == 0 {
		if len(snippets) == 0 {
			return "(no content)"
		}
		return strings.Join(snippets, " | ")
	}

	// 读取完整会话消息用于摘要上下文
	msgs, err := storage.GetOriginMessages(group.SessionID)
	if err != nil {
		logger.Warn("tool", "", group.SessionID, "session_search: get origin messages: "+err.Error(), 0)
		return strings.Join(snippets, " | ")
	}

	// 居中截断：以命中片段为锚点，取最多 SessionSearchSummaryMaxChars 字符的上下文窗口
	convText := centeredConversationContext(msgs, snippets, config.Cfg.SessionSearchSummaryMaxChars)
	if convText == "" {
		return strings.Join(snippets, " | ")
	}

	prompt := fmt.Sprintf(summaryPromptTmpl, query)
	summaryMessages := []llm.ChatMessage{
		{Role: "user", Content: convText + "\n\n---\n\n" + prompt},
	}

	summaryText, err := llmClient.ChatSync(ctx, summaryMessages)
	if err != nil {
		logger.Warn("tool", "", group.SessionID, "session_search: summary llm error: "+err.Error(), 0)
		return strings.Join(snippets, " | ")
	}
	return strings.TrimSpace(summaryText)
}

// centeredConversationContext 将 msgs 拼接为全文，然后以第一个命中片段（snippets[0]）
// 为锚点，取最多 maxChars 字节的居中窗口。
// 若全文本身不超过 maxChars，直接返回全文。
// 窗口边界对齐到行首（换行符），避免截断在消息中间。
func centeredConversationContext(msgs []storage.OriginSessionMessage, snippets []string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = config.Cfg.SessionSearchSummaryMaxChars
	}

	// 拼接全文
	var sb strings.Builder
	for _, m := range msgs {
		if m.Content == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
	}
	full := sb.String()

	if len(full) <= maxChars {
		return strings.TrimSpace(full)
	}

	// 找第一个命中片段在全文中的字节偏移（取前 120 字节做查找键，避免过长）
	anchorPos := -1
	for _, snip := range snippets {
		key := snip
		if len(key) > 120 {
			// 截到合法 UTF-8 边界
			key = key[:120]
			for len(key) > 0 && key[len(key)-1]&0xC0 == 0x80 {
				key = key[:len(key)-1]
			}
		}
		if idx := strings.Index(full, key); idx >= 0 {
			anchorPos = idx
			break
		}
	}

	// 未找到锚点：fallback 取末尾 maxChars
	if anchorPos < 0 {
		start := len(full) - maxChars
		// 对齐到行首
		if nl := strings.Index(full[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
		return "...(earlier context omitted)\n" + strings.TrimSpace(full[start:])
	}

	// 以锚点为中心计算窗口
	half := maxChars / 2
	start := anchorPos - half
	if start < 0 {
		start = 0
	}
	end := start + maxChars
	if end > len(full) {
		end = len(full)
		start = end - maxChars
		if start < 0 {
			start = 0
		}
	}

	// 将 start/end 对齐到行首/行尾，避免截断消息
	if start > 0 {
		if nl := strings.Index(full[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
	}
	if end < len(full) {
		if nl := strings.LastIndex(full[:end], "\n"); nl > start {
			end = nl + 1
		}
	}

	prefix, suffix := "", ""
	if start > 0 {
		prefix = "...(earlier context omitted)\n"
	}
	if end < len(full) {
		suffix = "\n...(later context omitted)"
	}
	return strings.TrimSpace(prefix + full[start:end] + suffix)
}
