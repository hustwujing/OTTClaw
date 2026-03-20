// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/feishu/dedup.go — 飞书消息幂等去重
//
// 飞书在服务响应超时或网络抖动时会重发相同 message_id 的消息事件，
// 若不做去重会导致：同一条消息被处理多次、重复写 DB、重复调用 LLM、重复发送回复。
//
// 实现：内存 map（messageID → 过期时间），TTL = 5 分钟（飞书重发窗口约 1-2 分钟）。
// 单进程部署，服务重启后缓存丢失，但重发场景均发生在服务运行期间，因此内存方案足够。
package feishu

import (
	"sync"
	"time"
)

const dedupTTL = 5 * time.Minute

var dedupStore = struct {
	mu   sync.Mutex
	seen map[string]time.Time // messageID → expiry
}{seen: make(map[string]time.Time)}

func init() {
	// 后台定期清理过期条目，防止内存持续增长
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			dedupStore.mu.Lock()
			for k, exp := range dedupStore.seen {
				if now.After(exp) {
					delete(dedupStore.seen, k)
				}
			}
			dedupStore.mu.Unlock()
		}
	}()
}

// isDuplicateMessage 原子地检查并标记 messageID。
// 首次出现时标记并返回 false（不重复，正常处理）；
// 已存在且未过期时返回 true（重复，应丢弃）。
// messageID 为空时始终返回 false（不过滤，避免因字段缺失导致漏处理）。
func isDuplicateMessage(messageID string) bool {
	if messageID == "" {
		return false
	}
	dedupStore.mu.Lock()
	defer dedupStore.mu.Unlock()
	if exp, ok := dedupStore.seen[messageID]; ok && time.Now().Before(exp) {
		return true
	}
	dedupStore.seen[messageID] = time.Now().Add(dedupTTL)
	return false
}
