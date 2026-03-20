// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/wecom/api.go — 企业微信群机器人 Webhook API 封装
//
// 企微 Webhook 消息格式与飞书略有差异：
//   - 请求路径：POST https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx
//   - 支持 msgtype：text / markdown
//   - 成功响应：{"errcode":0,"errmsg":"ok"}
package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"OTTClaw/config"
)

// send 向企微 Webhook URL 发送任意消息体
func send(ctx context.Context, webhookURL string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal wecom payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build wecom request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: time.Duration(config.Cfg.WeComWebhookTimeoutSec) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post wecom webhook: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("parse wecom response: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("wecom api error %d: %s", result.ErrCode, result.ErrMsg)
	}
	return nil
}

// SendText 通过 Webhook 发送纯文本消息
func SendText(ctx context.Context, webhookURL, text string) error {
	return send(ctx, webhookURL, map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	})
}

// SendMarkdown 通过 Webhook 发送 Markdown 消息
// 企微 Markdown 支持：粗体 **text**、斜体 *text*、链接 [name](url)、
// 颜色 <font color="info/comment/warning">text</font>、有序/无序列表、引用 >
func SendMarkdown(ctx context.Context, webhookURL, text string) error {
	return send(ctx, webhookURL, map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"content": text},
	})
}
