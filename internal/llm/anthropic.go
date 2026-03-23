// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/llm/anthropic.go — Anthropic Messages API 流式客户端
//
// 与 OpenAI 的主要格式差异：
//   - system 消息作为请求顶层字段，不放在 messages 数组
//   - assistant 工具调用使用 content 数组中的 tool_use block
//   - 工具调用结果使用 role=user 消息中的 tool_result block（多个连续结果合并为一条）
//   - 工具定义用 input_schema 替代 parameters
//   - 必须传 max_tokens；认证用 x-api-key 头和 anthropic-version 头
//   - SSE 事件格式：event: <type>\ndata: <json>
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"OTTClaw/config"
)

// ----- Anthropic 请求结构 -----

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string 或 []contentBlock
}

// contentBlock Anthropic content block（text / tool_use / tool_result）
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     *map[string]any `json:"input,omitempty"`       // tool_use — 指针类型：nil 时 omitempty 才真正省略；
	// 空 map{} 不能用 omitempty 省略，否则 Anthropic 报 "input: Field required"
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	Content   string          `json:"content,omitempty"`     // tool_result
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ----- anthropicClient 实现 -----

type anthropicClient struct {
	httpClient *http.Client
	apiKey     string
	model      string
	maxTokens  int
	baseURL    string
}

func newAnthropicClientFromEndpoint(ep config.LLMEndpointConfig) *anthropicClient {
	baseURL := ep.BaseURL
	if baseURL == "" || baseURL == "https://api.openai.com" {
		baseURL = "https://api.anthropic.com"
	}
	maxTokens := ep.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8096
	}
	return &anthropicClient{
		httpClient: &http.Client{
			Timeout:   120 * time.Second,
			Transport: streamTransport(),
		},
		apiKey:    ep.APIKey,
		model:     ep.Model,
		maxTokens: maxTokens,
		baseURL:   baseURL,
	}
}

func newAnthropicClient() *anthropicClient {
	return newAnthropicClientFromEndpoint(config.Cfg.LLMEndpoints[0])
}

// ChatSync 发起非流式请求（通过流式接口实现）
func (c *anthropicClient) ChatSync(ctx context.Context, messages []ChatMessage) (string, error) {
	eventCh, err := c.ChatStream(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	for ev := range eventCh {
		if ev.Error != nil {
			return "", ev.Error
		}
		if ev.Done {
			break
		}
		buf.WriteString(ev.TextChunk)
	}
	return buf.String(), nil
}

// ChatStream 发起流式请求，返回事件 channel
func (c *anthropicClient) ChatStream(ctx context.Context, messages []ChatMessage, tools []Tool) (<-chan StreamEvent, error) {
	system, anthropicMsgs, err := convertToAnthropicMessages(messages)
	if err != nil {
		return nil, fmt.Errorf("convert messages: %w", err)
	}

	req := anthropicRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    system,
		Messages:  anthropicMsgs,
		Stream:    true,
	}
	if len(tools) > 0 {
		req.Tools = convertToAnthropicTools(tools)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic api error %d: %s", resp.StatusCode, string(b))
	}

	events := make(chan StreamEvent, 1)
	go func() {
		defer resp.Body.Close()
		defer close(events)
		parseAnthropicStream(ctx, resp.Body, events)
	}()

	return events, nil
}

// ----- 消息格式转换 -----

// convertToAnthropicMessages 将 OpenAI 格式的 messages 转换为 Anthropic 格式。
// system 消息被提取为独立返回值；连续的 role=tool 消息被合并为单条 user 消息。
func convertToAnthropicMessages(messages []ChatMessage) (system string, result []anthropicMessage, err error) {
	var systemParts []string
	i := 0
	for i < len(messages) {
		msg := messages[i]
		switch msg.Role {
		case "system":
			systemParts = append(systemParts, msg.Content)
			i++

		case "user":
			if len(msg.Parts) > 0 {
				blocks := make([]map[string]any, 0, len(msg.Parts))
				for _, p := range msg.Parts {
					switch p.Type {
					case "text":
						blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
					case "image":
						blocks = append(blocks, map[string]any{
							"type": "image",
							"source": map[string]any{
								"type":       "base64",
								"media_type": p.MediaType,
								"data":       p.Data,
							},
						})
					}
				}
				content, _ := json.Marshal(blocks)
				result = append(result, anthropicMessage{Role: "user", Content: content})
			} else {
				content, _ := json.Marshal(msg.Content)
				result = append(result, anthropicMessage{Role: "user", Content: content})
			}
			i++

		case "assistant":
			if len(msg.ToolCalls) == 0 {
				// 纯文本 assistant 消息
				content, _ := json.Marshal(msg.Content)
				result = append(result, anthropicMessage{Role: "assistant", Content: content})
			} else {
				// 防御性检查：若 assistant 有 tool_use 但下一条消息不是 tool result，
				// 说明上一轮 Agent 在执行工具期间崩溃/超时，tool result 未写入 DB。
				// 降级为纯文本消息（保留文本内容，丢弃孤立的 tool_use blocks），
				// 避免 Anthropic 400 "tool_use without tool_result" 错误导致会话永久损坏。
				nextIsToolResult := i+1 < len(messages) && messages[i+1].Role == "tool"
				if !nextIsToolResult {
					text := msg.Content
					if text == "" {
						text = "[工具调用中断]"
					}
					content, _ := json.Marshal(text)
					result = append(result, anthropicMessage{Role: "assistant", Content: content})
					i++
					continue
				}
				// 含工具调用的 assistant 消息：构造 content block 数组
				var blocks []contentBlock
				if msg.Content != "" {
					blocks = append(blocks, contentBlock{Type: "text", Text: msg.Content})
				}
				for _, tc := range msg.ToolCalls {
					// 始终初始化为空 map，确保序列化为 {} 而非省略该字段
					// Anthropic 要求 tool_use block 的 input 字段必须存在，即使参数为空
					input := map[string]any{}
					if tc.Function.Arguments != "" {
						json.Unmarshal([]byte(tc.Function.Arguments), &input) //nolint:errcheck
					}
					blocks = append(blocks, contentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: &input,
					})
				}
				content, _ := json.Marshal(blocks)
				result = append(result, anthropicMessage{Role: "assistant", Content: content})
			}
			i++

		case "tool":
			// 将所有连续的 tool 消息合并为单条 role=user 消息（Anthropic 要求）
			// tool_result 的 content 支持字符串（纯文本）或 content block 数组（含图片）。
			var toolResults []map[string]any
			for i < len(messages) && messages[i].Role == "tool" {
				m := messages[i]
				tr := map[string]any{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
				}
				if len(m.Parts) > 0 {
					// 多模态 tool result：转为 Anthropic content block 数组
					blocks := make([]map[string]any, 0, len(m.Parts))
					for _, p := range m.Parts {
						switch p.Type {
						case "text":
							blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
						case "image":
							blocks = append(blocks, map[string]any{
								"type": "image",
								"source": map[string]any{
									"type":       "base64",
									"media_type": p.MediaType,
									"data":       p.Data,
								},
							})
						}
					}
					tr["content"] = blocks
				} else {
					tr["content"] = m.Content
				}
				toolResults = append(toolResults, tr)
				i++
			}
			content, _ := json.Marshal(toolResults)
			result = append(result, anthropicMessage{Role: "user", Content: content})

		default:
			i++
		}
	}

	system = strings.Join(systemParts, "\n\n")

	// 防御性清理：移除 result 开头的孤立 tool_result 消息。
	// 正常情况下 compressHistory 的 user 边界切割已保证不会出现此情况，
	// 此处作为最后一道防线，避免任何边界偏差导致 Anthropic 400 报错。
	for len(result) > 0 && isOrphanedToolResultMsg(result[0]) {
		result = result[1:]
	}

	return
}

// isOrphanedToolResultMsg 判断一条 Anthropic message 是否为全由 tool_result 块组成的 user 消息。
// 这类消息出现在 messages[0] 时意味着没有对应的前驱 assistant tool_use，Anthropic 会拒绝。
func isOrphanedToolResultMsg(msg anthropicMessage) bool {
	if msg.Role != "user" {
		return false
	}
	var blocks []map[string]any
	if err := json.Unmarshal(msg.Content, &blocks); err != nil || len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if t, ok := b["type"].(string); !ok || t != "tool_result" {
			return false
		}
	}
	return true
}

// convertToAnthropicTools 将 OpenAI 工具定义格式转换为 Anthropic 格式
func convertToAnthropicTools(tools []Tool) []anthropicTool {
	result := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		result = append(result, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return result
}

// ----- SSE 流解析 -----

// parseAnthropicStream 解析 Anthropic SSE 流，将文本 delta 和工具调用发送到 channel
func parseAnthropicStream(ctx context.Context, r io.Reader, events chan<- StreamEvent) {
	scanner := bufio.NewScanner(r)

	type partialToolBlock struct {
		ID       string
		Name     string
		ArgsAccu strings.Builder
	}

	// index → block type and accumulated data
	blockTypes := make(map[int]string)
	toolBlocks := make(map[int]*partialToolBlock)
	toolBlockCount := 0

	var (
		currentEvent  string
		dataLines     []string
		inputTokens   int
		outputTokens  int
	)

	send := func(ev StreamEvent) {
		select {
		case events <- ev:
		case <-ctx.Done():
		}
	}

	// processEvent 处理一个完整的 SSE 事件
	processEvent := func(eventType, dataStr string) {
		switch eventType {
		case "message_start":
			var d struct {
				Message struct {
					Usage struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(dataStr), &d); err == nil {
				inputTokens = d.Message.Usage.InputTokens
			}

		case "content_block_start":
			var d struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(dataStr), &d); err != nil {
				return
			}
			blockTypes[d.Index] = d.ContentBlock.Type
			if d.ContentBlock.Type == "tool_use" {
				toolBlocks[d.Index] = &partialToolBlock{
					ID:   d.ContentBlock.ID,
					Name: d.ContentBlock.Name,
				}
				toolBlockCount++
			}

		case "content_block_delta":
			var d struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(dataStr), &d); err != nil {
				return
			}
			switch d.Delta.Type {
			case "text_delta":
				if d.Delta.Text != "" {
					send(StreamEvent{TextChunk: d.Delta.Text})
				}
			case "input_json_delta":
				if tb, ok := toolBlocks[d.Index]; ok {
					tb.ArgsAccu.WriteString(d.Delta.PartialJSON)
				}
			}

		case "message_delta":
			var d struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(dataStr), &d); err != nil {
				return
			}
			if d.Usage.OutputTokens > 0 {
				outputTokens = d.Usage.OutputTokens
			}
			// 有工具调用时组装 ToolCalls 发送给 Agent
			if toolBlockCount > 0 {
				toolCalls := make([]ToolCall, 0, toolBlockCount)
				for idx := 0; idx < len(blockTypes); idx++ {
					if blockTypes[idx] != "tool_use" {
						continue
					}
					tb := toolBlocks[idx]
					toolCalls = append(toolCalls, ToolCall{
						ID:   tb.ID,
						Type: "function",
						Function: ToolCallFunction{
							Name:      tb.Name,
							Arguments: tb.ArgsAccu.String(),
						},
					})
				}
				if len(toolCalls) > 0 {
					send(StreamEvent{ToolCalls: toolCalls})
				}
			}

		case "message_stop":
			ev := StreamEvent{Done: true}
			if inputTokens > 0 || outputTokens > 0 {
				total := inputTokens + outputTokens
				ev.Usage = &Usage{
					PromptTokens:     inputTokens,
					CompletionTokens: outputTokens,
					TotalTokens:      total,
				}
			}
			send(ev)

		case "error":
			var d struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(dataStr), &d); err == nil && d.Error.Message != "" {
				send(StreamEvent{Error: fmt.Errorf("anthropic error: %s", d.Error.Message)})
			}
		}
	}

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// 空行：处理当前积累的事件
			if currentEvent != "" && len(dataLines) > 0 {
				processEvent(currentEvent, strings.Join(dataLines, "\n"))
			}
			currentEvent = ""
			dataLines = nil
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}

	// 处理末尾没有空行的情况
	if currentEvent != "" && len(dataLines) > 0 {
		processEvent(currentEvent, strings.Join(dataLines, "\n"))
	}

	if err := scanner.Err(); err != nil {
		send(StreamEvent{Error: fmt.Errorf("read anthropic stream: %w", err)})
	}
}
