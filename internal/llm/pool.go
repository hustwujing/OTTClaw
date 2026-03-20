// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/llm/pool.go — 多 LLM 节点 round-robin 负载均衡
//
// 当配置了多个 LLM 节点（LLM_BASE_URL_2、LLM_BASE_URL_3 ...）时，
// NewClient() 自动返回 poolClient，按 round-robin 策略将请求分发到各节点。
// 每个节点拥有独立的底层 Client（含各自的限速层）。
package llm

import (
	"context"
	"sync/atomic"
)

// poolClient 将多个 Client 以 round-robin 方式轮询
type poolClient struct {
	clients []Client
	counter atomic.Uint64
}

func (p *poolClient) pick() Client {
	idx := p.counter.Add(1) - 1
	return p.clients[idx%uint64(len(p.clients))]
}

func (p *poolClient) ChatSync(ctx context.Context, messages []ChatMessage) (string, error) {
	return p.pick().ChatSync(ctx, messages)
}

func (p *poolClient) ChatStream(ctx context.Context, messages []ChatMessage, tools []Tool) (<-chan StreamEvent, error) {
	return p.pick().ChatStream(ctx, messages, tools)
}
