// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/mcp/http.go — MCP Streamable HTTP transport 实现 (v1: 非流式 JSON)
//
// 向配置的 url 发 HTTP POST，Content-Type: application/json。
// v1 只处理非流式 JSON 响应（不支持 SSE 流）。
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

type httpClient struct {
	cfg    ServerConfig
	http   *http.Client
	idSeq  atomic.Int32
}

func newHTTPClient(cfg ServerConfig) *httpClient {
	return &httpClient{
		cfg: cfg,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Initialize HTTP transport 不需要特殊握手
func (c *httpClient) Initialize(_ context.Context) error {
	return nil
}

// ListTools 调用 tools/list
func (c *httpClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	result, err := c.sendRecv(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return parseToolList(result)
}

// CallTool 调用 tools/call
func (c *httpClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	result, err := c.sendRecv(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}
	return parseToolCallResult(result)
}

// Close HTTP client 无需清理
func (c *httpClient) Close() error {
	return nil
}

// sendRecv 向 MCP HTTP endpoint 发送 JSON-RPC 请求并返回结果
func (c *httpClient) sendRecv(ctx context.Context, method string, params any) (map[string]any, error) {
	id := int(c.idSeq.Add(1))
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp http: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp http: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp http: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp http: server returned %d: %s", resp.StatusCode, string(b))
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("mcp http: read response: %w", err)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("mcp http: parse response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("mcp http rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}
