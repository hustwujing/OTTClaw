// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/feishu/pending.go — 飞书会话交互等待状态（上传文件等需要二次交互的场景）
package feishu

import (
	"sync"
	"time"

	"OTTClaw/config"
)

// pendingKind 等待类型
type pendingKind int

const (
	PendingUpload pendingKind = iota + 1 // 等待用户上传图片
	PendingChoice                         // 等待用户选择/确认（文字回复）
)

type pendingEntry struct {
	kind      pendingKind
	expiresAt time.Time
}

var pendingStore = struct {
	mu   sync.Mutex
	data map[string]*pendingEntry // key: sessionID
}{data: make(map[string]*pendingEntry)}

func init() {
	// 后台定期清理过期条目
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			now := time.Now()
			pendingStore.mu.Lock()
			for k, e := range pendingStore.data {
				if now.After(e.expiresAt) {
					delete(pendingStore.data, k)
				}
			}
			pendingStore.mu.Unlock()
		}
	}()
}

// MarkPending 标记某会话正在等待特定类型的用户输入（30 分钟过期）
func MarkPending(sessionID string, kind pendingKind) {
	pendingStore.mu.Lock()
	defer pendingStore.mu.Unlock()
	pendingStore.data[sessionID] = &pendingEntry{
		kind:      kind,
		expiresAt: time.Now().Add(time.Duration(config.Cfg.FeishuPendingTimeoutMin) * time.Minute),
	}
}

// PopPending 取出并清除等待状态，返回 kind 和是否存在（一次性消费）
func PopPending(sessionID string) (pendingKind, bool) {
	pendingStore.mu.Lock()
	defer pendingStore.mu.Unlock()
	e, ok := pendingStore.data[sessionID]
	if !ok || time.Now().After(e.expiresAt) {
		delete(pendingStore.data, sessionID)
		return 0, false
	}
	delete(pendingStore.data, sessionID)
	return e.kind, true
}
