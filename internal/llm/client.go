// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/llm/client.go — LLM 客户端接口定义与工厂
//
// 支持两种 Provider（通过 LLM_PROVIDER 环境变量选择）：
//   - openai（默认）：OpenAI Chat Completions API，兼容所有 OpenAI 协议的服务
//   - anthropic：Anthropic Messages API
package llm

import (
	"context"
	"net/http"

	"OTTClaw/config"
)

// ----- 公共类型（Provider 无关）-----

// ContentPart 多模态消息中的单个内容块
type ContentPart struct {
	Type      string // "text" 或 "image"
	Text      string // Type=="text" 时有值
	MediaType string // Type=="image" 时，MIME 类型，如 "image/jpeg"
	Data      string // Type=="image" 时，base64 编码的图片数据
}

// ChatMessage 对话中的单条消息（OpenAI 格式在内部存储；发送给 Anthropic 时做转换）
type ChatMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	Parts      []ContentPart `json:"-"` // 多模态内容块；非空时优先于 Content
	ToolCallID string        `json:"tool_call_id,omitempty"` // role=tool 时填写
	Name       string        `json:"name,omitempty"`          // role=tool 时填写函数名
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`    // role=assistant 且有工具调用时填写
}

// ToolCall LLM 返回的工具调用请求
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction 工具调用中的函数信息
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON 字符串
}

// Tool 传给 LLM 的工具定义（OpenAI function calling 格式）
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction 函数定义
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// CacheBreakMarker 是注入 system prompt 中的零宽分隔符，用于标记 Anthropic prompt-cache 断点。
// Anthropic 客户端按此标记将 system 字段拆分为多个 content block，
// 每个断点之前的 block 加 cache_control: ephemeral，从而启用 prefix caching。
const CacheBreakMarker = "\x00\x00CB\x00\x00"

// Usage LLM 调用的 token 消耗统计
type Usage struct {
	PromptTokens        int
	CompletionTokens    int
	TotalTokens         int
	CacheReadTokens     int // Anthropic: cache_read_input_tokens；OpenAI: prompt_tokens_details.cached_tokens
	CacheCreationTokens int // Anthropic: cache_creation_input_tokens；OpenAI: 无对应字段
}

// StreamEvent Agent 消费的流事件
type StreamEvent struct {
	TextChunk string     // 普通文本 delta
	ToolCalls []ToolCall // 当模型决定调用工具时填充完整 tool calls
	Done      bool       // 流结束（Usage 在此事件中填充）
	Usage     *Usage     // token 消耗，仅 Done==true 时有值
	Error     error      // 出错
}

// ----- Client 接口 -----

// Client LLM 客户端接口，OpenAI 和 Anthropic 均实现此接口
type Client interface {
	// ChatSync 发起非流式请求，阻塞直到收到完整文本回复。
	// 仅用于内部摘要生成等不需要流式输出的场景，不传工具定义。
	ChatSync(ctx context.Context, messages []ChatMessage) (string, error)

	// ChatStream 发起流式请求，返回事件 channel。
	// 调用方需遍历 channel 直到收到 Done==true 或 Error!=nil。
	ChatStream(ctx context.Context, messages []ChatMessage, tools []Tool) (<-chan StreamEvent, error)
}

// newClientFromEndpoint 根据单个节点配置创建 Client（含可选限速层）。
func newClientFromEndpoint(ep config.LLMEndpointConfig) Client {
	var c Client
	switch ep.Provider {
	case "anthropic":
		c = newAnthropicClientFromEndpoint(ep)
	default: // "openai" 及一切兼容 OpenAI 协议的服务
		c = newOpenAIClientFromEndpoint(ep)
	}
	if ep.RPM > 0 {
		c = newRateLimitedClient(c, ep.RPM)
	}
	return c
}

// NewClient 根据全局配置创建对应 Provider 的客户端。
// 若配置了多个 LLM 节点（LLM_ENDPOINTS），返回 round-robin 池客户端；
// 否则返回单节点客户端。各节点独立配置限速（LLM_RPM[_N]）。
func NewClient() Client {
	endpoints := config.Cfg.LLMEndpoints
	if len(endpoints) <= 1 {
		// 单节点：走原有逻辑
		return newClientFromEndpoint(endpoints[0])
	}
	// 多节点：每个节点独立创建 Client，汇入 pool
	clients := make([]Client, 0, len(endpoints))
	for _, ep := range endpoints {
		clients = append(clients, newClientFromEndpoint(ep))
	}
	return &poolClient{clients: clients}
}

// streamTransport 返回一个基于 DefaultTransport 克隆的 Transport，
// 仅关闭自动 gzip 压缩。保留连接池、Keep-Alive、TLS 握手超时等默认配置。
// 禁用 gzip 是因为：若 LLM 代理返回 gzip 压缩的 SSE 流，Go 的 gzipReader
// 需要累积一个完整 deflate block 才能解压，导致流式 token 被缓冲。
func streamTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	return t
}
