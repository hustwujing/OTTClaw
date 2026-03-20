// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/mcp/types.go — MCP 公共类型定义
package mcp

import "time"

// ServerConfig 单个 MCP server 的配置
type ServerConfig struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Transport   string            `json:"transport"`                    // "stdio" | "http"
	Command     string            `json:"command"`                      // stdio only
	Args        []string          `json:"args"`                         // stdio only
	URL         string            `json:"url"`                          // http only
	Keywords    []string          `json:"keywords"`
	Env         map[string]string `json:"env"`
}

// MCPConfig 全局 MCP 配置文件结构
type MCPConfig struct {
	Servers []ServerConfig `json:"servers"`
}

// MCPTool 单个 MCP 工具描述
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ToolListCache tool list 缓存条目
type ToolListCache struct {
	Tools     []MCPTool
	FetchedAt time.Time
}

// ServerSummary 用于注入 system prompt 的 server 摘要
type ServerSummary struct {
	Name        string
	Description string
	Tools       []MCPTool
}

// ---- JSON-RPC 2.0 公共类型 ----

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *rpcError      `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
