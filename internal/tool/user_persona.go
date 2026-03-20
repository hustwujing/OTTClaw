// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/user_persona.go — 用户人设工具处理器
// 提供 2 个工具：get_user_persona / set_user_persona
package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"OTTClaw/internal/storage"
)

// handleGetUserPersona 查询当前用户的人设设置
func handleGetUserPersona(ctx context.Context, _ string) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}

	profile, err := storage.GetUserProfile(userID)
	if err != nil {
		return "", fmt.Errorf("get user profile: %w", err)
	}

	if profile == nil || profile.Persona == "" {
		b, _ := json.Marshal(map[string]any{"initialized": false})
		return string(b), nil
	}

	b, _ := json.Marshal(map[string]any{
		"initialized": true,
		"persona":     profile.Persona,
	})
	return string(b), nil
}

// handleUserPersona 通过 action 字段分发，替代 get_user_persona / set_user_persona 两个工具。
// action: get / set
func handleUserPersona(ctx context.Context, argsJSON string) (string, error) {
	var base struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &base); err != nil {
		return "", fmt.Errorf("parse user_persona action: %w", err)
	}
	switch base.Action {
	case "get":
		return handleGetUserPersona(ctx, argsJSON)
	case "set":
		return handleSetUserPersona(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown user_persona action: %q (valid: get/set)", base.Action)
	}
}

// handleSetUserPersona 创建或更新当前用户的人设
func handleSetUserPersona(ctx context.Context, argsJSON string) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}

	var args struct {
		Persona string `json:"persona"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse set_user_persona args: %w", err)
	}
	if args.Persona == "" {
		return "", fmt.Errorf("persona is required")
	}

	if err := storage.UpsertUserProfile(userID, args.Persona); err != nil {
		return "", fmt.Errorf("save user persona: %w", err)
	}

	b, _ := json.Marshal(map[string]any{"ok": true})
	return string(b), nil
}
