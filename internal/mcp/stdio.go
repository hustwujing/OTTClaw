// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/mcp/stdio.go — MCP stdio transport 实现
//
// 用 os/exec.Cmd 启动 MCP 进程，通过 stdin/stdout 进行 newline-delimited JSON-RPC。
// 懒连接：首次调用时 connect()，断线后自动 reconnect()。
// MCP 初始化握手：send initialize → receive response → send notifications/initialized。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"

	"OTTClaw/config"
)

type stdioClient struct {
	cfg    ServerConfig
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	scanner *bufio.Scanner
	idSeq  atomic.Int32
	ready  bool
}

func newStdioClient(cfg ServerConfig) *stdioClient {
	return &stdioClient{cfg: cfg}
}

// connect 启动子进程、建立管道并完成 MCP 握手（调用前须持有 mu）。
// 首次连接和重连（子进程崩溃后 ready=false）均走此路径，保证握手始终完成。
// 注意：子进程使用 context.Background() 启动，生命周期与 server 一致，
// 不受单次 HTTP 请求 ctx 的 cancel 影响；单次 RPC 超时由调用方 ctx 控制。
func (c *stdioClient) connect(ctx context.Context) error {
	if c.ready {
		return nil
	}
	args := c.cfg.Args
	cmd := exec.CommandContext(context.Background(), c.cfg.Command, args...)

	// 注入环境变量：优先级 os.Environ > dotEnvCache > cfg.Env
	// dotEnvCache 确保 .env 文件中的配置对子进程可见；
	// 系统环境变量（export 或 Docker 注入）始终具有最高优先级。
	{
		dotEnv := config.DotEnv()
		// 以 map 去重，按优先级从低到高写入（后写的覆盖先写的）
		merged := make(map[string]string)
		for k, v := range c.cfg.Env {
			merged[k] = v
		}
		for k, v := range dotEnv {
			merged[k] = v
		}
		// os.Environ 最高优先级：解析系统 KV 后写入
		for _, kv := range cmd.Environ() {
			for i := 0; i < len(kv); i++ {
				if kv[i] == '=' {
					merged[kv[:i]] = kv[i+1:]
					break
				}
			}
		}
		env := make([]string, 0, len(merged))
		for k, v := range merged {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp stdio: StdinPipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp stdio: StdoutPipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcp stdio: start %q: %w", c.cfg.Command, err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.scanner = bufio.NewScanner(stdout)
	c.ready = true

	// MCP 握手：initialize + notifications/initialized
	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "OTTClaw",
			"version": "1.0",
		},
	}
	if _, err := c.sendRecv(ctx, "initialize", initParams); err != nil {
		c.ready = false
		return fmt.Errorf("mcp stdio initialize: %w", err)
	}
	notif := rpcRequest{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "notifications/initialized",
	}
	b, _ := json.Marshal(notif)
	b = append(b, '\n')
	if _, err := c.stdin.Write(b); err != nil {
		c.ready = false
		return fmt.Errorf("mcp stdio notifications/initialized: %w", err)
	}

	return nil
}

// Initialize 建立连接并完成 MCP 握手
func (c *stdioClient) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connect(ctx)
}

// ListTools 列出该 server 所有工具
func (c *stdioClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.ready {
		if err := c.connect(ctx); err != nil {
			return nil, err
		}
	}

	result, err := c.sendRecv(ctx, "tools/list", map[string]any{})
	// 子进程崩溃（broken pipe / connection closed）时透明重连重试一次
	if err != nil && !c.ready {
		if err2 := c.connect(ctx); err2 == nil {
			result, err = c.sendRecv(ctx, "tools/list", map[string]any{})
		}
	}
	if err != nil {
		return nil, err
	}

	return parseToolList(result)
}

// CallTool 调用指定工具，返回文本结果
func (c *stdioClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.ready {
		if err := c.connect(ctx); err != nil {
			return "", err
		}
	}

	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	result, err := c.sendRecv(ctx, "tools/call", params)
	// 子进程崩溃（broken pipe / connection closed）时透明重连重试一次
	if err != nil && !c.ready {
		if err2 := c.connect(ctx); err2 == nil {
			result, err = c.sendRecv(ctx, "tools/call", params)
		}
	}
	if err != nil {
		return "", err
	}

	return parseToolCallResult(result)
}

// Close 关闭进程
func (c *stdioClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ready = false
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return nil
}

// sendRecv 发送 JSON-RPC 请求并等待响应（持有 mu 时调用）
func (c *stdioClient) sendRecv(ctx context.Context, method string, params any) (map[string]any, error) {
	id := int(c.idSeq.Add(1))
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal rpc request: %w", err)
	}
	b = append(b, '\n')

	if _, err := c.stdin.Write(b); err != nil {
		c.ready = false
		return nil, fmt.Errorf("write rpc request: %w", err)
	}

	// Read response lines until we get one matching our id
	for c.scanner.Scan() {
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.ID != id {
			continue // skip notifications or other responses
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
	if err := c.scanner.Err(); err != nil {
		c.ready = false
		return nil, fmt.Errorf("read rpc response: %w", err)
	}
	c.ready = false
	return nil, fmt.Errorf("mcp stdio: connection closed")
}

// parseToolList 从 tools/list 结果中提取 []MCPTool
func parseToolList(result map[string]any) ([]MCPTool, error) {
	if result == nil {
		return nil, nil
	}
	raw, ok := result["tools"]
	if !ok {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var tools []MCPTool
	if err := json.Unmarshal(b, &tools); err != nil {
		return nil, fmt.Errorf("parse tool list: %w", err)
	}
	return tools, nil
}

// parseToolCallResult 从 tools/call 结果中提取文本内容
func parseToolCallResult(result map[string]any) (string, error) {
	if result == nil {
		return "", nil
	}
	// Standard MCP response: {content: [{type:"text", text:"..."}]}
	if content, ok := result["content"]; ok {
		b, _ := json.Marshal(content)
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(b, &parts); err == nil && len(parts) > 0 {
			texts := make([]string, 0, len(parts))
			for _, p := range parts {
				if p.Text != "" {
					texts = append(texts, p.Text)
				}
			}
			if len(texts) > 0 {
				result := ""
				for i, t := range texts {
					if i > 0 {
						result += "\n"
					}
					result += t
				}
				return result, nil
			}
		}
	}
	// Fallback: return full JSON
	b, _ := json.Marshal(result)
	return string(b), nil
}
