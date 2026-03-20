// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/feishu.go — 飞书相关工具处理器
// 提供 4 个工具：feishu_send / feishu_webhook / get_feishu_config / set_feishu_config
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"OTTClaw/internal/feishu"
	"OTTClaw/internal/storage"
)

// handleFeishuSend 通过飞书 Bot API 向指定接收方发送文本消息或文件。
// receive_id 传 "self" 时自动解析为当前用户绑定的飞书 open_id。
func handleFeishuSend(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ReceiveID     string `json:"receive_id"`
		ReceiveIDType string `json:"receive_id_type"` // open_id | user_id | chat_id | union_id
		Text          string `json:"text"`
		FilePath      string `json:"file_path"` // 本地文件路径，与 text 二选一
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse feishu_send args: %w", err)
	}
	if args.ReceiveID == "" {
		return "", fmt.Errorf("receive_id is required（传 \"self\" 可自动使用当前用户绑定的 user_id）")
	}
	if args.Text == "" && args.FilePath == "" {
		return "", fmt.Errorf("text or file_path is required")
	}
	if args.ReceiveIDType == "" {
		args.ReceiveIDType = "open_id"
	}

	// "self" → 解析为当前用户绑定的飞书 ID，并根据前缀自动推断类型
	if args.ReceiveID == "self" {
		userID := userIDFromCtx(ctx)
		if userID == "" {
			return "", fmt.Errorf("user_id not found in context")
		}
		selfID, err := storage.GetSelfOpenID(userID)
		if err != nil {
			return "", fmt.Errorf("get self open_id: %w", err)
		}
		if selfID == "" {
			return "", fmt.Errorf("当前用户尚未绑定飞书 ID，请先调用 set_feishu_config 设置 self_open_id 字段")
		}
		args.ReceiveID = selfID
		args.ReceiveIDType = inferFeishuIDType(selfID)
	}

	// 发送文件（图片或普通文件）
	if args.FilePath != "" {
		if feishu.IsImagePath(args.FilePath) {
			imageKey, err := feishu.UploadImage(args.FilePath)
			if err != nil {
				return "", fmt.Errorf("upload image: %w", err)
			}
			if err := feishu.SendImageTo(args.ReceiveID, args.ReceiveIDType, imageKey); err != nil {
				return "", fmt.Errorf("feishu send image: %w", err)
			}
		} else {
			fileKey, err := feishu.UploadFile(args.FilePath, "")
			if err != nil {
				return "", fmt.Errorf("upload file: %w", err)
			}
			if err := feishu.SendFileTo(args.ReceiveID, args.ReceiveIDType, fileKey); err != nil {
				return "", fmt.Errorf("feishu send file: %w", err)
			}
		}
		return `"ok"`, nil
	}

	// 发送文本
	if err := feishu.SendTextTo(args.ReceiveID, args.ReceiveIDType, args.Text); err != nil {
		return "", fmt.Errorf("feishu send: %w", err)
	}
	return `"ok"`, nil
}

// handleFeishuWebhook 通过 Webhook URL 发送文本消息
func handleFeishuWebhook(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		WebhookURL string `json:"webhook_url"`
		Text       string `json:"text"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse feishu_webhook args: %w", err)
	}
	if args.WebhookURL == "" {
		return "", fmt.Errorf("webhook_url is required")
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	if err := feishu.PostWebhook(args.WebhookURL, args.Text); err != nil {
		return "", fmt.Errorf("feishu webhook: %w", err)
	}
	return `"ok"`, nil
}

// handleGetFeishuConfig 读取当前用户的飞书配置（AppSecret 脱敏，self_open_id 完整返回）
func handleGetFeishuConfig(ctx context.Context, argsJSON string) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}

	cfg, err := storage.GetFeishuConfig(userID)
	if err != nil {
		return "", fmt.Errorf("get feishu config: %w", err)
	}
	if cfg == nil {
		b, _ := json.Marshal(map[string]any{
			"configured": false,
			"message":    "飞书机器人尚未配置，请调用 set_feishu_config 进行配置",
		})
		return string(b), nil
	}

	maskedSecret := ""
	if cfg.AppSecretEnc != "" {
		maskedSecret = "****（已设置）"
	}

	b, _ := json.Marshal(map[string]any{
		"configured":   cfg.AppID != "" && cfg.AppSecretEnc != "",
		"app_id":       cfg.AppID,
		"app_secret":   maskedSecret,
		"webhook_url":  cfg.WebhookURL,
		"self_open_id": cfg.SelfOpenID, // 用于 feishu_send(receive_id="self")
		"updated_at":   cfg.UpdatedAt,
	})
	return string(b), nil
}

// handleSetFeishuConfig 写入或更新飞书配置，并重启长连接
func handleSetFeishuConfig(ctx context.Context, argsJSON string) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}

	var args struct {
		AppID       string `json:"app_id"`
		AppSecret   string `json:"app_secret"`   // 明文，本工具负责加密
		WebhookURL  string `json:"webhook_url"`
		SelfOpenID  string `json:"self_open_id"` // 用户自己的飞书 open_id
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse set_feishu_config args: %w", err)
	}

	isBotMode := args.AppID != "" || args.AppSecret != ""
	if isBotMode && args.AppID == "" {
		return "", fmt.Errorf("app_id is required when app_secret is provided")
	}

	// 合并已有配置：未传字段保留原值
	existing, _ := storage.GetFeishuConfig(userID)
	appID := args.AppID
	webhookURL := args.WebhookURL
	if existing != nil {
		if appID == "" {
			appID = existing.AppID
		}
		if webhookURL == "" {
			webhookURL = existing.WebhookURL
		}
	}

	if err := storage.SetFeishuConfig(userID, appID, args.AppSecret, webhookURL, args.SelfOpenID); err != nil {
		return "", fmt.Errorf("save feishu config: %w", err)
	}

	feishu.InvalidateToken()

	if appID != "" {
		newCfg, _ := storage.GetFeishuConfig(userID)
		if newCfg != nil && newCfg.AppSecretEnc != "" {
			feishu.Registry.StartForUser(context.Background(), userID, newCfg)
		}
	}

	result := map[string]any{"status": "ok", "message": "配置已保存"}
	if isBotMode {
		result["message"] = "配置已保存，飞书长连接已重启"
	}

	details := []string{}
	if appID != "" {
		details = append(details, fmt.Sprintf("App ID: %s", appID))
	}
	if args.AppSecret != "" {
		details = append(details, "App Secret: 已更新（加密存储）")
	}
	if webhookURL != "" {
		details = append(details, fmt.Sprintf("Webhook URL: %s", maskURL(webhookURL)))
	}
	if args.SelfOpenID != "" {
		details = append(details, fmt.Sprintf("Self Open ID: %s", args.SelfOpenID))
	}
	if len(details) > 0 {
		result["details"] = strings.Join(details, "；")
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// maskURL 脱敏 URL
func maskURL(u string) string {
	if len(u) <= 20 {
		return u[:4] + "****"
	}
	return u[:20] + "****"
}

// handleFeishu 通过 action 字段分发到各飞书操作处理器，替代 4 个独立工具。
// action: send / webhook / get_config / set_config
func handleFeishu(ctx context.Context, argsJSON string) (string, error) {
	var base struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &base); err != nil {
		return "", fmt.Errorf("parse feishu action: %w", err)
	}
	switch base.Action {
	case "send":
		return handleFeishuSend(ctx, argsJSON)
	case "webhook":
		return handleFeishuWebhook(ctx, argsJSON)
	case "get_config":
		return handleGetFeishuConfig(ctx, argsJSON)
	case "set_config":
		return handleSetFeishuConfig(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown feishu action: %q (valid: send/webhook/get_config/set_config)", base.Action)
	}
}

// inferFeishuIDType 根据飞书 ID 前缀自动推断 receive_id_type，
// 避免因用户存储的 ID 类型与发送时指定的类型不匹配导致 API 报错。
//   - ou_  → open_id（应用维度用户 ID，最常见）
//   - on_  → union_id（跨应用联合 ID）
//   - oc_  → chat_id（群 ID）
//   - 其他 → user_id（企业内部员工 ID，纯数字或字母）
func inferFeishuIDType(id string) string {
	switch {
	case strings.HasPrefix(id, "ou_"):
		return "open_id"
	case strings.HasPrefix(id, "on_"):
		return "union_id"
	case strings.HasPrefix(id, "oc_"):
		return "chat_id"
	default:
		return "user_id"
	}
}
