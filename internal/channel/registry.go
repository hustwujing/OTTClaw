// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/channel/registry.go — 通用 Registry：管理每用户长连接生命周期
package channel

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

// Registry 持有一个 Adapter，管理每用户的长连接生命周期。
// 带指数退避的重连由框架统一处理，Adapter 只负责单次连接。
type Registry struct {
	adapter Adapter
	runner  AgentRunFunc
	mu      sync.Mutex
	conns   map[string]context.CancelFunc
	stopped bool
}

// NewRegistry 创建 Registry
func NewRegistry(adapter Adapter) *Registry {
	return &Registry{
		adapter: adapter,
		conns:   make(map[string]context.CancelFunc),
	}
}

// SetAgentRunner 注入 agent 执行函数（服务启动后调用）
func (r *Registry) SetAgentRunner(fn AgentRunFunc) {
	r.runner = fn
}

// StartAll 为所有已配置用户启动长连接（服务启动时调用）
func (r *Registry) StartAll(ctx context.Context) {
	userIDs, err := r.adapter.GetConfiguredUserIDs()
	if err != nil {
		logger.Warn(r.adapter.Name(), "", "", fmt.Sprintf("get configured users: %v", err), 0)
		return
	}
	for _, uid := range userIDs {
		r.StartForUser(ctx, uid)
	}
}

// StartForUser 为指定用户启动（或重启）长连接
func (r *Registry) StartForUser(ctx context.Context, ownerUserID string) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	if cancel, ok := r.conns[ownerUserID]; ok {
		cancel()
	}
	ctx2, cancel := context.WithCancel(ctx)
	r.conns[ownerUserID] = cancel
	r.mu.Unlock()

	go r.runWithReconnect(ctx2, ownerUserID)
}

// StopForUser 停止指定用户的长连接
func (r *Registry) StopForUser(ownerUserID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancel, ok := r.conns[ownerUserID]; ok {
		cancel()
		delete(r.conns, ownerUserID)
	}
}

// StopAll 停止所有长连接（服务关闭时调用）。
// 调用后 StartForUser 将成为 no-op，防止 shutdown 窗口期创建新 goroutine。
func (r *Registry) StopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	for uid, cancel := range r.conns {
		cancel()
		delete(r.conns, uid)
	}
}

// Dispatch 直接派发一条消息给 agent，不经过连接生命周期管理。
// 供渠道内部（如飞书 RunForSession）在已知 peer/writerFactory 时直接触发 agent 运行。
func (r *Registry) Dispatch(ctx context.Context, ownerUserID, peerID, userText string, wf WriterFactory) {
	name := r.adapter.Name()
	sessionID, err := storage.GetOrCreateChannelSession(name, ownerUserID, peerID)
	if err != nil {
		logger.Error(name, ownerUserID, "", "get session", err, 0)
		return
	}
	if r.runner == nil {
		logger.Warn(name, ownerUserID, sessionID, "agent runner not set", 0)
		return
	}
	w := wf(sessionID)
	runner := r.runner
	go func() {
		if c, ok := w.(interface{ Close() }); ok {
			defer c.Close()
		}
		if err := runner(ctx, ownerUserID, sessionID, userText, w); err != nil {
			logger.Error(name, ownerUserID, sessionID, "agent run", err, 0)
		}
	}()
}

// runWithReconnect 带指数退避的重连循环
func (r *Registry) runWithReconnect(ctx context.Context, ownerUserID string) {
	name := r.adapter.Name()
	backoff := time.Second
	const maxBackoff = 5 * time.Minute

	for {
		if ctx.Err() != nil {
			return
		}
		dispatch := r.makeDispatch(ctx, ownerUserID)
		err := r.adapter.Connect(ctx, ownerUserID, dispatch)
		if ctx.Err() != nil {
			// ctx 取消（StopForUser/StopAll），正常退出
			logger.Info(name, ownerUserID, "", "connection stopped", 0)
			return
		}
		if err != nil {
			logger.Warn(name, ownerUserID, "",
				fmt.Sprintf("connection error: %v, retrying in %s", err, backoff), 0)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
	}
}

// makeDispatch 创建 DispatchFunc，adapter 收到消息时通过此函数分发
func (r *Registry) makeDispatch(ctx context.Context, ownerUserID string) DispatchFunc {
	name := r.adapter.Name()
	return func(msgCtx context.Context, peerID, userText string, wf WriterFactory) {
		sessionID, err := storage.GetOrCreateChannelSession(name, ownerUserID, peerID)
		if err != nil {
			logger.Error(name, ownerUserID, "", "get session", err, 0)
			return
		}
		if r.runner == nil {
			logger.Warn(name, ownerUserID, sessionID, "agent runner not set", 0)
			return
		}
		w := wf(sessionID)
		runner := r.runner
		go func() {
			if c, ok := w.(interface{ Close() }); ok {
				defer c.Close()
			}
			if err := runner(msgCtx, ownerUserID, sessionID, userText, w); err != nil {
				logger.Error(name, ownerUserID, sessionID, "agent run", err, 0)
			}
		}()
	}
}
