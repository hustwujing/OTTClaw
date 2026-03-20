// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/tool/user_memory.go — 用户记忆工具
// 提供 update_user_memory 工具，支持超限自动压缩
package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"OTTClaw/config"
	"OTTClaw/internal/storage"
)

// MemoryCompressor 调用 LLM 压缩记忆文本的函数类型，由 agent.Run() 注入
type MemoryCompressor func(ctx context.Context, text string, maxChars int) (string, error)

type memoryCompressorCtxKey struct{}

// WithMemoryCompressor 将 MemoryCompressor 注入 context
func WithMemoryCompressor(ctx context.Context, c MemoryCompressor) context.Context {
	return context.WithValue(ctx, memoryCompressorCtxKey{}, c)
}

func memoryCompressorFromCtx(ctx context.Context) MemoryCompressor {
	c, _ := ctx.Value(memoryCompressorCtxKey{}).(MemoryCompressor)
	return c
}

// handleUpdateUserMemory 更新用户记忆，超限时自动调 LLM 压缩
func handleUpdateUserMemory(ctx context.Context, argsJSON string) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}

	var args struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse update_user_memory args: %w", err)
	}
	if args.Content == "" {
		return "", fmt.Errorf("content is required")
	}

	maxChars := config.Cfg.UserMemoryMaxChars
	content := args.Content
	compressed := false

	// 超限时自动压缩
	if len([]rune(content)) > maxChars {
		compressor := memoryCompressorFromCtx(ctx)
		if compressor == nil {
			return "", fmt.Errorf("memory exceeds limit (%d chars) but compressor not available", maxChars)
		}
		var err error
		content, err = compressor(ctx, content, maxChars)
		if err != nil {
			return "", fmt.Errorf("compress memory: %w", err)
		}
		compressed = true
	}

	if err := storage.UpdateUserMemory(userID, content); err != nil {
		return "", fmt.Errorf("save user memory: %w", err)
	}

	b, _ := json.Marshal(map[string]any{"ok": true, "compressed": compressed})
	return string(b), nil
}
