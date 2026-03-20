// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/mcp.go — mcp 工具处理器
//
// action=list   : 列出所有 server 及其工具摘要（含 description）
// action=detail : 返回 server.tool 的完整 inputSchema
// action=call   : 调用 server.tool，返回结果文本
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"OTTClaw/internal/mcp"
)

// handleMCP 处理 mcp 工具调用
func handleMCP(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Action string         `json:"action"`
		Server string         `json:"server"`
		Tool   string         `json:"tool"`
		Args   map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("mcp: parse args: %w", err)
	}

	reg := mcp.Global
	if reg == nil {
		return `{"error":"mcp_disabled","message":"MCP is not configured. Create config/mcp.json to enable MCP servers."}`, nil
	}

	switch args.Action {
	case "list":
		return mcpList(ctx, reg)
	case "detail":
		if args.Server == "" || args.Tool == "" {
			return "", fmt.Errorf("mcp: action=detail requires server and tool")
		}
		return mcpDetail(ctx, reg, args.Server, args.Tool)
	case "call":
		if args.Server == "" || args.Tool == "" {
			return "", fmt.Errorf("mcp: action=call requires server and tool")
		}
		if args.Args == nil {
			args.Args = map[string]any{}
		}
		return mcpCall(ctx, reg, args.Server, args.Tool, args.Args)
	default:
		return "", fmt.Errorf("mcp: unknown action %q (use list/detail/call)", args.Action)
	}
}

// mcpList 列出所有 server 及其工具（尝试从缓存或拉取）
func mcpList(ctx context.Context, reg *mcp.Registry) (string, error) {
	servers := reg.Servers()
	if len(servers) == 0 {
		return `{"servers":[],"message":"No MCP servers configured."}`, nil
	}

	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	type serverInfo struct {
		Name        string     `json:"name"`
		Description string     `json:"description"`
		Transport   string     `json:"transport"`
		Tools       []toolInfo `json:"tools,omitempty"`
		Error       string     `json:"error,omitempty"`
	}

	result := make([]serverInfo, 0, len(servers))
	for _, s := range servers {
		info := serverInfo{
			Name:        s.Name,
			Description: s.Description,
			Transport:   s.Transport,
		}
		tools, err := reg.ListTools(ctx, s.Name)
		if err != nil {
			info.Error = err.Error()
		} else {
			info.Tools = make([]toolInfo, 0, len(tools))
			for _, t := range tools {
				info.Tools = append(info.Tools, toolInfo{Name: t.Name, Description: t.Description})
			}
		}
		result = append(result, info)
	}

	b, _ := json.MarshalIndent(map[string]any{"servers": result}, "", "  ")
	return string(b), nil
}

// mcpDetail 返回指定工具的完整 inputSchema
func mcpDetail(ctx context.Context, reg *mcp.Registry, serverName, toolName string) (string, error) {
	tool, err := reg.GetTool(ctx, serverName, toolName)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(tool, "", "  ")
	return string(b), nil
}

// mcpCall 调用指定工具并返回结果
func mcpCall(ctx context.Context, reg *mcp.Registry, serverName, toolName string, args map[string]any) (string, error) {
	result, err := reg.CallTool(ctx, serverName, toolName, args)
	if err != nil {
		return "", err
	}
	// 若结果不是 JSON，包装成 JSON 返回
	trimmed := strings.TrimSpace(result)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		b, _ := json.Marshal(map[string]string{"result": result})
		return string(b), nil
	}
	return result, nil
}
