// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/llm/ratelimit.go — 基于令牌桶的 LLM 请求限速包装器
//
// 当 LLM_RPM > 0 时，NewClient() 会自动将底层客户端包装为 rateLimitedClient。
// 每次 ChatStream / ChatSync 调用前等待令牌可用，超出速率时阻塞（而非丢弃请求）。
//
// 令牌桶参数：
//   - 速率 r = RPM / 60（每秒令牌数）
//   - 桶容量 b = max(1, RPM/10)，允许少量突发，避免连续慢请求积累后引发突发
package llm

import (
	"context"
	"fmt"

	"golang.org/x/time/rate"
)

// rateLimitedClient 包装任意 Client，在每次调用前等待令牌
type rateLimitedClient struct {
	inner   Client
	limiter *rate.Limiter
}

// newRateLimitedClient 创建限速包装器。rpm 为每分钟最大请求数。
func newRateLimitedClient(inner Client, rpm int) Client {
	r := rate.Limit(float64(rpm) / 60.0)
	burst := rpm / 10
	if burst < 1 {
		burst = 1
	}
	return &rateLimitedClient{
		inner:   inner,
		limiter: rate.NewLimiter(r, burst),
	}
}

func (c *rateLimitedClient) ChatSync(ctx context.Context, messages []ChatMessage) (string, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("llm rate limiter: %w", err)
	}
	return c.inner.ChatSync(ctx, messages)
}

func (c *rateLimitedClient) ChatStream(ctx context.Context, messages []ChatMessage, tools []Tool) (<-chan StreamEvent, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("llm rate limiter: %w", err)
	}
	return c.inner.ChatStream(ctx, messages, tools)
}
