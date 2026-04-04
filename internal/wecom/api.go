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
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

// SendImage 通过 Webhook 发送本地图片（base64 编码，限 2MB）
func SendImage(ctx context.Context, webhookURL, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}
	if len(data) > 2*1024*1024 {
		return fmt.Errorf("图片超过 2MB 限制（当前 %.1fMB），请压缩后再试", float64(len(data))/(1024*1024))
	}
	sum := md5.Sum(data)
	return send(ctx, webhookURL, map[string]any{
		"msgtype": "image",
		"image": map[string]string{
			"base64": base64.StdEncoding.EncodeToString(data),
			"md5":    fmt.Sprintf("%x", sum),
		},
	})
}

// SendFile 通过 Webhook 发送本地文件（先上传拿 media_id，再发消息）
func SendFile(ctx context.Context, webhookURL, filePath string) error {
	// 从 webhook URL 提取 key（企微上传接口复用同一 key）
	u, err := url.Parse(webhookURL)
	if err != nil {
		return fmt.Errorf("parse webhook url: %w", err)
	}
	key := u.Query().Get("key")
	if key == "" {
		return fmt.Errorf("webhook_url 中缺少 key 参数")
	}

	mediaID, err := uploadMedia(ctx, key, filePath)
	if err != nil {
		return fmt.Errorf("upload file: %w", err)
	}
	return send(ctx, webhookURL, map[string]any{
		"msgtype": "file",
		"file":    map[string]string{"media_id": mediaID},
	})
}

// uploadMedia 上传文件到企微 Webhook 媒体接口，返回 media_id
func uploadMedia(ctx context.Context, key, filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	uploadURL := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/webhook/upload_media?key=%s&type=file",
		url.QueryEscape(key))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("media", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", fmt.Errorf("copy file: %w", err)
	}
	w.Close()

	client := &http.Client{Timeout: time.Duration(config.Cfg.WeComWebhookTimeoutSec) * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload request: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result struct {
		ErrCode  int    `json:"errcode"`
		ErrMsg   string `json:"errmsg"`
		MediaID  string `json:"media_id"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse upload response: %w", err)
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("wecom upload error %d: %s", result.ErrCode, result.ErrMsg)
	}
	return result.MediaID, nil
}
