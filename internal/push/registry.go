// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/push/registry.go — 基于 session 的服务端主动推送通道
// 用于 cron 任务完成后实时通知前端，前端通过 GET /api/notify 订阅
package push

import "sync"

// Registry 维护 sessionID → 订阅者 channel 列表的映射
type Registry struct {
	mu    sync.RWMutex
	chans map[string][]chan []byte
}

// Default 全局默认注册表
var Default = &Registry{chans: make(map[string][]chan []byte)}

// Subscribe 为指定 session 注册一个订阅 channel，返回只读 channel 和取消函数
func (r *Registry) Subscribe(sessionID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	r.mu.Lock()
	r.chans[sessionID] = append(r.chans[sessionID], ch)
	r.mu.Unlock()

	cancel := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		list := r.chans[sessionID]
		for i, c := range list {
			if c == ch {
				r.chans[sessionID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(r.chans[sessionID]) == 0 {
			delete(r.chans, sessionID)
		}
		close(ch)
	}
	return ch, cancel
}

// Publish 向指定 session 的所有订阅者广播事件（非阻塞，缓冲满则丢弃）
func (r *Registry) Publish(sessionID string, data []byte) {
	r.mu.RLock()
	list := append([]chan []byte(nil), r.chans[sessionID]...) // 拷贝，避免长时间持锁
	r.mu.RUnlock()
	for _, ch := range list {
		select {
		case ch <- data:
		default: // 缓冲满则丢弃，不阻塞调用方
		}
	}
}

// HasSubscribers 返回指定 session 是否有活跃订阅者
func (r *Registry) HasSubscribers(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.chans[sessionID]) > 0
}
