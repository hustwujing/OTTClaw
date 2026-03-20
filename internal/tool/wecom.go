// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/wecom.go — 企业微信相关工具处理器
// 提供 3 个工具：wecom_send / get_wecom_config / set_wecom_config
package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"OTTClaw/internal/storage"
	"OTTClaw/internal/wecom"
)

// handleWeComSend 通过企业微信群机器人 Webhook 发送消息
func handleWeComSend(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		WebhookURL string `json:"webhook_url"`
		Text       string `json:"text"`
		MsgType    string `json:"msgtype"` // "text"（默认）或 "markdown"
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse wecom_send args: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	// 未指定 webhook_url 时从存储读取
	webhookURL := args.WebhookURL
	if webhookURL == "" {
		userID := userIDFromCtx(ctx)
		if userID == "" {
			return "", fmt.Errorf("webhook_url is required（或先用 set_wecom_config 保存）")
		}
		cfg, err := storage.GetWeComConfig(userID)
		if err != nil {
			return "", fmt.Errorf("get wecom config: %w", err)
		}
		if cfg == nil || cfg.WebhookURL == "" {
			return "", fmt.Errorf("尚未配置企业微信 Webhook，请先调用 set_wecom_config 或在参数中传入 webhook_url")
		}
		webhookURL = cfg.WebhookURL
	}

	var err error
	if args.MsgType == "markdown" {
		err = wecom.SendMarkdown(ctx, webhookURL, args.Text)
	} else {
		err = wecom.SendText(ctx, webhookURL, args.Text)
	}
	if err != nil {
		return "", fmt.Errorf("send wecom message: %w", err)
	}
	return `"ok"`, nil
}

// handleGetWeComConfig 读取当前用户的企微机器人配置
func handleGetWeComConfig(ctx context.Context, _ string) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}
	cfg, err := storage.GetWeComConfig(userID)
	if err != nil {
		return "", fmt.Errorf("get wecom config: %w", err)
	}

	result := map[string]any{
		"configured":  false,
		"webhook_url": "",
		"updated_at":  nil,
	}
	if cfg != nil && cfg.WebhookURL != "" {
		result["configured"] = true
		result["webhook_url"] = maskURL(cfg.WebhookURL) // 脱敏：显示前 20 字符
		result["updated_at"] = cfg.UpdatedAt
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// handleWecom 通过 action 字段分发到各企业微信操作处理器，替代 3 个独立工具。
// action: send / get_config / set_config
func handleWecom(ctx context.Context, argsJSON string) (string, error) {
	var base struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &base); err != nil {
		return "", fmt.Errorf("parse wecom action: %w", err)
	}
	switch base.Action {
	case "send":
		return handleWeComSend(ctx, argsJSON)
	case "get_config":
		return handleGetWeComConfig(ctx, argsJSON)
	case "set_config":
		return handleSetWeComConfig(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown wecom action: %q (valid: send/get_config/set_config)", base.Action)
	}
}

// handleSetWeComConfig 写入企微 Webhook URL
func handleSetWeComConfig(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		WebhookURL string `json:"webhook_url"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse set_wecom_config args: %w", err)
	}
	if args.WebhookURL == "" {
		return "", fmt.Errorf("webhook_url is required")
	}

	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}
	if err := storage.SetWeComConfig(userID, args.WebhookURL); err != nil {
		return "", fmt.Errorf("save wecom config: %w", err)
	}

	b, _ := json.Marshal(map[string]any{
		"status":      "ok",
		"webhook_url": maskURL(args.WebhookURL),
	})
	return string(b), nil
}
