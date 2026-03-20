// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/mcp/registry.go — MCP server 注册表，懒初始化 client + tool list 缓存
//
// 隔离策略：
//   - stdio server：每个 agent session 独立一个子进程，会话结束时 CloseSession 自动回收
//   - http  server：全局共享连接，HTTP 层自行处理并发
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const toolListTTL = 5 * time.Minute

// ========== Context 注入：sessionID ==========

type mcpSessionKey struct{}

// WithSessionID 将 sessionID 注入 context，Registry 用它为 stdio server 创建 per-session 子进程
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, mcpSessionKey{}, sessionID)
}

func sessionIDFromCtx(ctx context.Context) string {
	s, _ := ctx.Value(mcpSessionKey{}).(string)
	return s
}

// stdioClientKey 生成 stdio client 的 map key
func stdioClientKey(serverName, sessionID string) string {
	return serverName + ":" + sessionID
}

// Registry 管理所有 MCP server 的配置、client 连接和工具列表缓存
type Registry struct {
	mu          sync.RWMutex
	servers     []ServerConfig
	clients     map[string]Client    // key → client（stdio: "name:sessionID"，http: "name"）
	sessionKeys map[string][]string  // sessionID → []clientKey，用于 CloseSession 快速回收
	cache       map[string]*ToolListCache // tool list 缓存，按 serverName 共享（schema 与用户无关）
}

// Global 全局 Registry 单例，服务启动时初始化
var Global *Registry

// Load 从 mcp.json 文件加载 MCP server 配置
func (r *Registry) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read mcp config %q: %w", path, err)
	}
	var cfg MCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse mcp config: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers = cfg.Servers
	r.clients = make(map[string]Client)
	r.sessionKeys = make(map[string][]string)
	r.cache = make(map[string]*ToolListCache)
	return nil
}

// CloseSession 关闭并回收指定 session 所有 stdio 子进程
// 由 agent.Run defer 调用，确保会话结束后进程不残留
func (r *Registry) CloseSession(sessionID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	keys := r.sessionKeys[sessionID]
	delete(r.sessionKeys, sessionID)
	clients := make([]Client, 0, len(keys))
	for _, key := range keys {
		if c, ok := r.clients[key]; ok {
			clients = append(clients, c)
			delete(r.clients, key)
		}
	}
	r.mu.Unlock()

	// 在锁外关闭，避免 Close 阻塞时持锁
	for _, c := range clients {
		_ = c.Close()
	}
}

// MatchIntent 根据用户消息关键词返回匹配的 server 摘要列表
func (r *Registry) MatchIntent(userMsg string) []ServerSummary {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	servers := r.servers
	r.mu.RUnlock()

	lower := strings.ToLower(userMsg)
	var matched []ServerSummary
	for _, s := range servers {
		for _, kw := range s.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				sum := ServerSummary{
					Name:        s.Name,
					Description: s.Description,
				}
				r.mu.RLock()
				if c, ok := r.cache[s.Name]; ok && time.Since(c.FetchedAt) < toolListTTL {
					sum.Tools = c.Tools
				}
				r.mu.RUnlock()
				matched = append(matched, sum)
				break
			}
		}
	}
	return matched
}

// ListTools 返回指定 server 的工具列表（带缓存）
func (r *Registry) ListTools(ctx context.Context, serverName string) ([]MCPTool, error) {
	if r == nil {
		return nil, fmt.Errorf("mcp registry not initialized")
	}

	r.mu.RLock()
	if c, ok := r.cache[serverName]; ok && time.Since(c.FetchedAt) < toolListTTL {
		tools := c.Tools
		r.mu.RUnlock()
		return tools, nil
	}
	r.mu.RUnlock()

	client, err := r.getOrCreateClient(ctx, serverName)
	if err != nil {
		return nil, err
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp list tools %q: %w", serverName, err)
	}

	r.mu.Lock()
	r.cache[serverName] = &ToolListCache{
		Tools:     tools,
		FetchedAt: time.Now(),
	}
	r.mu.Unlock()

	return tools, nil
}

// CallTool 调用指定 server 的工具
func (r *Registry) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	if r == nil {
		return "", fmt.Errorf("mcp registry not initialized")
	}

	client, err := r.getOrCreateClient(ctx, serverName)
	if err != nil {
		return "", err
	}

	result, err := client.CallTool(ctx, toolName, args)
	if err != nil {
		return "", fmt.Errorf("mcp call %q.%q: %w", serverName, toolName, err)
	}
	return result, nil
}

// GetTool 返回指定 server.tool 的 MCPTool（用于 action=detail）
func (r *Registry) GetTool(ctx context.Context, serverName, toolName string) (*MCPTool, error) {
	tools, err := r.ListTools(ctx, serverName)
	if err != nil {
		return nil, err
	}
	for i := range tools {
		if tools[i].Name == toolName {
			return &tools[i], nil
		}
	}
	return nil, fmt.Errorf("tool %q not found in server %q", toolName, serverName)
}

// BuildPromptSection 生成 system prompt 注入段
func (r *Registry) BuildPromptSection(servers []ServerSummary) string {
	if len(servers) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, s := range servers {
		if len(s.Tools) == 0 {
			sb.WriteString(fmt.Sprintf("[%s] %s (tools not yet loaded — use mcp(action=list, server=%q) to fetch)\n",
				s.Name, s.Description, s.Name))
			continue
		}
		names := make([]string, 0, len(s.Tools))
		for _, t := range s.Tools {
			names = append(names, t.Name)
		}
		sb.WriteString(fmt.Sprintf("[%s] %s: %s (%d tools)\n",
			s.Name, s.Description, strings.Join(names, ", "), len(s.Tools)))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// Servers 返回当前加载的所有 server 配置列表（只读快照）
func (r *Registry) Servers() []ServerConfig {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]ServerConfig, len(r.servers))
	copy(result, r.servers)
	return result
}

// getOrCreateClient 懒初始化并返回指定 server 的 Client
//   - stdio：key = "serverName:sessionID"，per-session 独立子进程
//   - http ：key = "serverName"，全局共享
func (r *Registry) getOrCreateClient(ctx context.Context, serverName string) (Client, error) {
	// Find server config (needed to determine transport)
	r.mu.RLock()
	var cfg *ServerConfig
	for i := range r.servers {
		if r.servers[i].Name == serverName {
			cfg = &r.servers[i]
			break
		}
	}
	r.mu.RUnlock()
	if cfg == nil {
		return nil, fmt.Errorf("mcp server %q not found in config", serverName)
	}

	key := serverName
	sessionID := ""
	if cfg.Transport == "stdio" {
		sessionID = sessionIDFromCtx(ctx)
		key = stdioClientKey(serverName, sessionID)
	}

	// Fast path
	r.mu.RLock()
	client, ok := r.clients[key]
	r.mu.RUnlock()
	if ok {
		return client, nil
	}

	// Create and initialize
	c := newClient(*cfg)
	if err := c.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("mcp initialize %q: %w", serverName, err)
	}

	r.mu.Lock()
	// Double-check after acquiring write lock
	if existing, ok := r.clients[key]; ok {
		r.mu.Unlock()
		_ = c.Close()
		return existing, nil
	}
	r.clients[key] = c
	if cfg.Transport == "stdio" && sessionID != "" {
		r.sessionKeys[sessionID] = append(r.sessionKeys[sessionID], key)
	}
	r.mu.Unlock()

	return c, nil
}
