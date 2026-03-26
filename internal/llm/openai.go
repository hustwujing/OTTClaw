// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/llm/openai.go — OpenAI Chat Completions 流式客户端
// 兼容所有使用 OpenAI API 协议的服务（如 Azure OpenAI、本地 ollama 等）
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"OTTClaw/config"
)

// ----- 请求结构（OpenAI 专用）-----

// openAIMessage OpenAI 消息：Content 支持 string（文本）或 []any（多模态）
type openAIMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"` // string 或 []map[string]any
	ToolCallID string      `json:"tool_call_id,omitempty"`
	Name       string      `json:"name,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
}

// toOpenAIMessages 将内部 ChatMessage 转换为 OpenAI API 格式（含多模态支持）
func toOpenAIMessages(messages []ChatMessage) []openAIMessage {
	result := make([]openAIMessage, 0, len(messages))
	for _, m := range messages {
		msg := openAIMessage{
			Role:       m.Role,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
			ToolCalls:  m.ToolCalls,
		}
		if len(m.Parts) > 0 {
			parts := make([]map[string]any, 0, len(m.Parts))
			for _, p := range m.Parts {
				switch p.Type {
				case "text":
					parts = append(parts, map[string]any{"type": "text", "text": p.Text})
				case "image":
					parts = append(parts, map[string]any{
						"type": "image_url",
						"image_url": map[string]string{
							"url": "data:" + p.MediaType + ";base64," + p.Data,
						},
					})
				}
			}
			msg.Content = parts
		} else {
			msg.Content = m.Content
		}
		result = append(result, msg)
	}
	return result
}

// openAIRequest Chat Completions 请求体
type openAIRequest struct {
	Model         string          `json:"model"`
	Messages      []openAIMessage `json:"messages"`
	Stream        bool            `json:"stream"`
	Tools         []Tool          `json:"tools,omitempty"`
	MaxTokens     int             `json:"max_tokens,omitempty"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}

// ----- 响应结构（流式 chunk 解析）-----

type openAIDelta struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolCalls []struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type openAIChoice struct {
	Delta        openAIDelta `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIChunk struct {
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage"`
}

// ----- openAIClient 实现 -----

type openAIClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	maxTokens  int
}

func newOpenAIClientFromEndpoint(ep config.LLMEndpointConfig) *openAIClient {
	return &openAIClient{
		httpClient: &http.Client{
			Timeout:   0, // 流式请求不设 HTTP 层超时，依赖 ctx 取消；固定 120s 会在 thinking 阶段把连接强杀
			Transport: streamTransport(),
		},
		baseURL:   ep.BaseURL,
		apiKey:    ep.APIKey,
		model:     ep.Model,
		maxTokens: ep.MaxTokens,
	}
}

func newOpenAIClient() *openAIClient {
	return newOpenAIClientFromEndpoint(config.Cfg.LLMEndpoints[0])
}

// openAIFullResponse 非流式 Chat Completions 响应体
type openAIFullResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// ChatSync 发起非流式请求（stream=false），阻塞直到收到完整文本回复。
// 不使用 ChatStream，避免部分代理对 stream=true 请求内部处理失败（500 "expected stream response"）。
func (c *openAIClient) ChatSync(ctx context.Context, messages []ChatMessage) (string, error) {
	req := openAIRequest{
		Model:    c.model,
		Messages: toOpenAIMessages(messages),
		Stream:   false,
	}
	if c.maxTokens > 0 {
		req.MaxTokens = c.maxTokens
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm api error %d: %s", resp.StatusCode, string(b))
	}
	var full openAIFullResponse
	if err := json.Unmarshal(b, &full); err != nil {
		return "", fmt.Errorf("parse sync response: %w", err)
	}
	if len(full.Choices) == 0 {
		return "", fmt.Errorf("empty choices in sync response")
	}
	return full.Choices[0].Message.Content, nil
}

// ChatStream 发起流式请求，返回事件 channel。
// stream_options 默认关闭：部分代理（如 bilibili llmapi）会静默接受该字段，
// 但为了计算 token 用量会缓冲整个响应，导致"出几个字→卡住→全量倾泻"。
// 若将来需要 usage 统计，可改为 true 并确认目标代理的流式行为。
func (c *openAIClient) ChatStream(ctx context.Context, messages []ChatMessage, tools []Tool) (<-chan StreamEvent, error) {
	return c.doStream(ctx, messages, tools, false)
}

// doStream 实际发起一次流式 HTTP 请求
func (c *openAIClient) doStream(ctx context.Context, messages []ChatMessage, tools []Tool, withStreamOptions bool) (<-chan StreamEvent, error) {
	req := openAIRequest{
		Model:    c.model,
		Messages: toOpenAIMessages(messages),
		Stream:   true,
		Tools:    tools,
	}
	if withStreamOptions {
		req.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}
	if c.maxTokens > 0 {
		req.MaxTokens = c.maxTokens
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
	httpReq.Header.Set("X-Accel-Buffering", "no")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("llm api error %d: %s", resp.StatusCode, string(b))
	}

	events := make(chan StreamEvent, 1)
	go func() {
		defer resp.Body.Close()
		defer close(events)
		parseOpenAIStream(ctx, resp.Body, events)
	}()

	return events, nil
}

// parseOpenAIStream 逐行读取 SSE 流，累积 tool_calls，将 text delta 和最终 tool_calls 发送到 channel
func parseOpenAIStream(ctx context.Context, r io.Reader, events chan<- StreamEvent) {
	scanner := bufio.NewScanner(r)
	// 单行上限扩大到 1 MB，防止工具调用参数过长时 Scanner 报 "token too long"
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	type partialCall struct {
		ID       string
		Type     string
		Name     string
		ArgsAccu strings.Builder
	}
	callMap := make(map[int]*partialCall)
	var lastUsage *openAIUsage

	send := func(ev StreamEvent) {
		select {
		case events <- ev:
		case <-ctx.Done():
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			ev := StreamEvent{Done: true}
			if lastUsage != nil {
				ev.Usage = &Usage{
					PromptTokens:     lastUsage.PromptTokens,
					CompletionTokens: lastUsage.CompletionTokens,
					TotalTokens:      lastUsage.TotalTokens,
				}
			}
			send(ev)
			return
		}

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		// usage chunk（choices 为空，只含 usage 统计）
		if chunk.Usage != nil {
			lastUsage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		delta := choice.Delta

		if delta.Content != "" {
			send(StreamEvent{TextChunk: delta.Content})
		}

		for _, tc := range delta.ToolCalls {
			if _, exists := callMap[tc.Index]; !exists {
				callMap[tc.Index] = &partialCall{}
			}
			p := callMap[tc.Index]
			if tc.ID != "" {
				p.ID = tc.ID
			}
			if tc.Type != "" {
				p.Type = tc.Type
			}
			if tc.Function.Name != "" {
				p.Name = tc.Function.Name
			}
			p.ArgsAccu.WriteString(tc.Function.Arguments)
		}

		if choice.FinishReason == "tool_calls" {
			indices := make([]int, 0, len(callMap))
			for idx := range callMap {
				indices = append(indices, idx)
			}
			sort.Ints(indices)
			toolCalls := make([]ToolCall, 0, len(indices))
			for _, idx := range indices {
				p := callMap[idx]
				toolCalls = append(toolCalls, ToolCall{
					ID:   p.ID,
					Type: p.Type,
					Function: ToolCallFunction{
						Name:      p.Name,
						Arguments: p.ArgsAccu.String(),
					},
				})
			}
			send(StreamEvent{ToolCalls: toolCalls})
		}
	}

	if err := scanner.Err(); err != nil {
		send(StreamEvent{Error: fmt.Errorf("read openai stream: %w", err)})
	}
}
