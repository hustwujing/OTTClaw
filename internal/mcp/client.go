// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/mcp/client.go — MCP Client 接口定义与工厂函数
package mcp

import "context"

// Client MCP client 接口，stdio / http 均实现此接口
type Client interface {
	// Initialize 建立连接并完成 MCP 握手
	Initialize(ctx context.Context) error
	// ListTools 列出该 server 所有工具
	ListTools(ctx context.Context) ([]MCPTool, error)
	// CallTool 调用指定工具，返回文本结果
	CallTool(ctx context.Context, name string, args map[string]any) (string, error)
	// Close 关闭连接（释放子进程或 HTTP 资源）
	Close() error
}

// newClient 根据 ServerConfig 创建对应 transport 的 Client
func newClient(cfg ServerConfig) Client {
	switch cfg.Transport {
	case "http":
		return newHTTPClient(cfg)
	default: // "stdio"
		return newStdioClient(cfg)
	}
}
