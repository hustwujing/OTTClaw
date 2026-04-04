// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/llm/context_cache.go — 显式 Context Cache 支持（Kimi / GLM / Doubao / Qwen）
//
// 开关：LLM_CONTEXT_CACHE_ENABLED=true
// 仅对能通过模型名前缀检测的 provider 生效；其余 provider 静默跳过。
package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"OTTClaw/config"
	"OTTClaw/internal/logger"
)

// cacheHTTPClient 专用于 cache 创建请求，设置 30s 超时避免阻塞主链路。
var cacheHTTPClient = &http.Client{Timeout: 30 * time.Second}

// detectExplicitCacheProvider 根据模型名前缀判断支持显式缓存的 provider。
// 返回 "kimi" / "glm" / "doubao" / "qwen"，不匹配则返回空字符串。
func detectExplicitCacheProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "moonshot-") || strings.HasPrefix(model, "kimi-"):
		return "kimi"
	case strings.HasPrefix(model, "glm-"):
		return "glm"
	case strings.HasPrefix(model, "doubao-") || strings.HasPrefix(model, "ep-"):
		return "doubao"
	case strings.HasPrefix(model, "qwen-"):
		return "qwen"
	}
	return ""
}

// sha256sum 返回字符串内容的 hex 编码 SHA-256 摘要。
func sha256sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// cacheProviderTTL 返回指定 provider 的本地缓存 TTL（略短于供应商侧实际有效期）。
// 值通过环境变量配置（LLM_CACHE_TTL_KIMI / GLM / DOUBAO / QWEN），默认值见 config.go。
func cacheProviderTTL(provider string) time.Duration {
	var sec int
	switch provider {
	case "kimi":
		sec = config.Cfg.LLMCacheTTLKimi
	case "glm":
		sec = config.Cfg.LLMCacheTTLGLM
	case "doubao":
		sec = config.Cfg.LLMCacheTTLDoubao
	case "qwen":
		sec = config.Cfg.LLMCacheTTLQwen
	default:
		sec = 3500
	}
	return time.Duration(sec) * time.Second
}

// cacheEntry 内存中缓存条目，含有效期。
type cacheEntry struct {
	id        string
	expiresAt time.Time // 过期时刻；Zero 值表示永不过期
}

// ContextCacheManager 管理显式 cache 条目的内存映射。
//
// key 格式：baseURL + "|" + model + "|" + sha256(staticContent)
// 以内容哈希为 key，天然实现用户隔离：
//   - 不同用户（技能集合不同）→ 不同 hash → 独立 cache entry
//   - 相同技能集合的用户（如仅含系统技能）→ 相同 hash → 共享 cache（正确且经济）
type ContextCacheManager struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry // key → cacheEntry
}

// GlobalCacheManager 全局单例。
var GlobalCacheManager = &ContextCacheManager{entries: make(map[string]cacheEntry)}

// GetOrCreate 返回未过期的已有 cacheID，或调用 createExplicitCache 创建新 cache 后写入内存。
// 过期判定采用本地时钟（cacheProviderTTL），略早于供应商侧实际失效时间，避免带着 stale ID 请求报错。
func (m *ContextCacheManager) GetOrCreate(ctx context.Context, baseURL, apiKey, model, staticContent string) (string, error) {
	h := sha256sum(staticContent)
	key := baseURL + "|" + model + "|" + h

	m.mu.RLock()
	entry, ok := m.entries[key]
	m.mu.RUnlock()
	if ok {
		if entry.expiresAt.IsZero() || time.Now().Before(entry.expiresAt) {
			logger.Debug("llm-cache", "", "",
				fmt.Sprintf("cache hit model=%s id=%s expires_in=%.0fs",
					model, entry.id, time.Until(entry.expiresAt).Seconds()), 0)
			return entry.id, nil
		}
		logger.Info("llm-cache", "", "",
			fmt.Sprintf("cache expired model=%s id=%s, recreating", model, entry.id), 0)
	} else {
		logger.Info("llm-cache", "", "",
			fmt.Sprintf("cache miss model=%s hash=%s", model, h[:8]), 0)
	}

	provider := detectExplicitCacheProvider(model)
	id, err := createExplicitCache(ctx, baseURL, apiKey, model, provider, staticContent)
	if err != nil {
		return "", err
	}

	ttl := cacheProviderTTL(provider)
	m.mu.Lock()
	m.entries[key] = cacheEntry{
		id:        id,
		expiresAt: time.Now().Add(ttl),
	}
	m.mu.Unlock()
	logger.Info("llm-cache", "", "",
		fmt.Sprintf("cache stored model=%s id=%s ttl=%s", model, id, ttl), 0)
	return id, nil
}

// createExplicitCache 向对应 provider 的 cache 创建接口发起 HTTP 请求，返回 cacheID。
//
// 各 provider 接口汇总：
//
//	Kimi:   POST {base}/v1/caching          body: {model, messages:[{role:system,content}], ttl:3600}  → .id
//	GLM:    POST {base}/v4/context/create   body: {model, messages:[{role:system,content}]}            → .id
//	Doubao: POST {base}/api/v3/context_caches body: {model, messages:[{role:system,content}]}          → .id
//	Qwen:   POST {base}/compatible-mode/v1/caches body: {model, messages:[{role:system,content}]}      → .id
func createExplicitCache(ctx context.Context, baseURL, apiKey, model, provider, staticContent string) (string, error) {
	sysMsg := map[string]any{"role": "system", "content": staticContent}

	var endpoint string
	var bodyMap map[string]any

	switch provider {
	case "kimi":
		endpoint = baseURL + "/v1/caching"
		bodyMap = map[string]any{
			"model":    model,
			"messages": []map[string]any{sysMsg},
			"ttl":      3600,
		}
	case "glm":
		endpoint = baseURL + "/v4/context/create"
		bodyMap = map[string]any{
			"model":    model,
			"messages": []map[string]any{sysMsg},
		}
	case "doubao":
		endpoint = baseURL + "/api/v3/context_caches"
		bodyMap = map[string]any{
			"model":    model,
			"messages": []map[string]any{sysMsg},
		}
	case "qwen":
		endpoint = baseURL + "/compatible-mode/v1/caches"
		bodyMap = map[string]any{
			"model":    model,
			"messages": []map[string]any{sysMsg},
		}
	default:
		return "", fmt.Errorf("unsupported provider for explicit cache: %q", provider)
	}

	body, err := json.Marshal(bodyMap)
	if err != nil {
		return "", fmt.Errorf("marshal cache request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build cache request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	logger.Info("llm-cache", "", "",
		fmt.Sprintf("creating cache provider=%s model=%s endpoint=%s", provider, model, endpoint), 0)
	start := time.Now()
	resp, err := cacheHTTPClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http cache create: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cache create error %d: %s", resp.StatusCode, string(b))
	}

	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		return "", fmt.Errorf("parse cache response: %w", err)
	}
	id, ok := result["id"].(string)
	if !ok || id == "" {
		return "", fmt.Errorf("missing id in cache response: %s", string(b))
	}
	logger.Info("llm-cache", "", "",
		fmt.Sprintf("cache created provider=%s model=%s id=%s cost=%s", provider, model, id, time.Since(start).Round(time.Millisecond)), 0)
	return id, nil
}
