// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/feishu/api.go — 飞书 Open API 封装：token 缓存、消息发送、消息更新、Webhook
package feishu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"OTTClaw/config"
)

// ── Token 缓存 ────────────────────────────────────────────────────────────────

type tokenCache struct {
	mu        sync.Mutex
	appID     string
	appSecret string
	token     string
	expiresAt time.Time
}

var globalTokenCache = &tokenCache{}

// SetCredentials 更新 Bot 凭证并清空缓存的 token
func SetCredentials(appID, appSecret string) {
	globalTokenCache.mu.Lock()
	defer globalTokenCache.mu.Unlock()
	globalTokenCache.appID = appID
	globalTokenCache.appSecret = appSecret
	globalTokenCache.token = ""
	globalTokenCache.expiresAt = time.Time{}
}

// GetToken 返回有效的 tenant_access_token，过期提前 5 分钟刷新
func GetToken() (string, error) {
	globalTokenCache.mu.Lock()
	defer globalTokenCache.mu.Unlock()

	if time.Now().Before(globalTokenCache.expiresAt) {
		return globalTokenCache.token, nil
	}

	if globalTokenCache.appID == "" || globalTokenCache.appSecret == "" {
		return "", fmt.Errorf("feishu credentials not configured")
	}

	body, _ := json.Marshal(map[string]string{
		"app_id":     globalTokenCache.appID,
		"app_secret": globalTokenCache.appSecret,
	})
	resp, err := http.Post(
		config.Cfg.FeishuAPIBase+"/open-apis/auth/v3/tenant_access_token/internal",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("get token: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("feishu api error %d: %s", result.Code, result.Msg)
	}

	globalTokenCache.token = result.TenantAccessToken
	globalTokenCache.expiresAt = time.Now().Add(time.Duration(result.Expire-300) * time.Second)
	return globalTokenCache.token, nil
}

// InvalidateToken 清空 token 缓存（凭证更新后调用）
func InvalidateToken() {
	globalTokenCache.mu.Lock()
	defer globalTokenCache.mu.Unlock()
	globalTokenCache.token = ""
	globalTokenCache.expiresAt = time.Time{}
}

// ── 消息发送 ──────────────────────────────────────────────────────────────────

// SendTextTo 向指定接收方发送文本消息
// receiveID: open_id / user_id / chat_id / union_id
// receiveIDType: "open_id" | "user_id" | "chat_id" | "union_id"
func SendTextTo(receiveID, receiveIDType, text string) error {
	token, err := GetToken()
	if err != nil {
		return err
	}

	content, _ := json.Marshal(map[string]string{"text": text})
	body, _ := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(content),
	})

	url := fmt.Sprintf(config.Cfg.FeishuAPIBase+"/open-apis/im/v1/messages?receive_id_type=%s", receiveIDType)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()
	return parseAPIError(resp.Body)
}

// buildTextCard 将纯文本/Markdown 包装为最简飞书交互卡片 JSON 字符串。
// 使用 lark_md 标签，支持粗体、链接等基础 Markdown 格式。
// 飞书 PATCH 更新消息 API 仅支持卡片消息，因此 ack 和最终回复都用卡片发送。
func buildTextCard(text string) string {
	card := map[string]any{
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": text,
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

// sendMessageGetID 发送消息并返回 message_id，供内部复用。
func sendMessageGetID(receiveID, receiveIDType, msgType, content string) (string, error) {
	token, err := GetToken()
	if err != nil {
		return "", err
	}

	body, _ := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   msgType,
		"content":    content,
	})

	url := fmt.Sprintf(config.Cfg.FeishuAPIBase+"/open-apis/im/v1/messages?receive_id_type=%s", receiveIDType)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse send response: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("feishu api error %d: %s", result.Code, result.Msg)
	}
	return result.Data.MessageID, nil
}

// SendCardGetID 以交互卡片形式发送文本消息，返回 message_id 供后续 UpdateCard 使用。
// 飞书只允许对卡片消息执行 PATCH 更新，因此需要等待回复的场景都用此函数发送。
func SendCardGetID(receiveID, receiveIDType, text string) (string, error) {
	return sendMessageGetID(receiveID, receiveIDType, "interactive", buildTextCard(text))
}

// UpdateCard 将已发送的交互卡片消息更新为新文本内容（PATCH）。
func UpdateCard(messageID, text string) error {
	token, err := GetToken()
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]string{
		"msg_type": "interactive",
		"content":  buildTextCard(text),
	})

	url := fmt.Sprintf(config.Cfg.FeishuAPIBase+"/open-apis/im/v1/messages/%s", messageID)
	req, _ := http.NewRequest("PATCH", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("update card: %w", err)
	}
	defer resp.Body.Close()
	return parseAPIError(resp.Body)
}

// SendOptionsCard 向指定对话发送带按钮的选项卡片
// chatID 可以是 chat_id 或 open_id，receiveIDType 对应类型
func SendOptionsCard(chatID, receiveIDType, title string, options []map[string]string) error {
	token, err := GetToken()
	if err != nil {
		return err
	}

	// 构建卡片按钮列表
	actions := make([]map[string]any, 0, len(options))
	for _, opt := range options {
		actions = append(actions, map[string]any{
			"tag":   "button",
			"text":  map[string]string{"tag": "plain_text", "content": opt["label"]},
			"type":  "default",
			"value": map[string]string{"__choice__": opt["value"]},
		})
	}

	card := map[string]any{
		"config": map[string]bool{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]string{"tag": "plain_text", "content": title},
			"template": "blue",
		},
		"elements": []any{
			map[string]any{
				"tag":     "action",
				"actions": actions,
			},
		},
	}
	cardJSON, _ := json.Marshal(card)

	body, _ := json.Marshal(map[string]string{
		"receive_id": chatID,
		"msg_type":   "interactive",
		"content":    string(cardJSON),
	})

	url := fmt.Sprintf(config.Cfg.FeishuAPIBase+"/open-apis/im/v1/messages?receive_id_type=%s", receiveIDType)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send card: %w", err)
	}
	defer resp.Body.Close()
	return parseAPIError(resp.Body)
}

// PostWebhook 向 Webhook URL 发送文本消息
func PostWebhook(webhookURL, text string) error {
	body, _ := json.Marshal(map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	})
	resp, err := http.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer resp.Body.Close()
	return parseAPIError(resp.Body)
}

// ── 文件上传与发送 ──────────────────────────────────────────────────────────────

// imageExts 图片扩展名集合（小写）
var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".webp": true, ".bmp": true,
}

// IsImagePath 判断路径是否为图片（按扩展名）
func IsImagePath(path string) bool {
	return imageExts[strings.ToLower(filepath.Ext(path))]
}

// feishuFileType 根据扩展名返回飞书文件类型标识
func feishuFileType(ext string) string {
	switch strings.ToLower(ext) {
	case ".pdf":
		return "pdf"
	case ".doc", ".docx":
		return "doc"
	case ".xls", ".xlsx":
		return "xls"
	case ".ppt", ".pptx":
		return "ppt"
	case ".mp4":
		return "mp4"
	case ".mp3":
		return "mp3"
	default:
		return "stream"
	}
}

// UploadImage 将本地图片上传到飞书，返回 image_key
func UploadImage(filePath string) (string, error) {
	token, err := GetToken()
	if err != nil {
		return "", err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open image: %w", err)
	}
	defer f.Close()

	// 根据扩展名推断 MIME 类型；飞书要求 multipart 中的 image 字段携带正确的 Content-Type，
	// 否则返回 error 234011 "Can't recognize image format"。
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath)))
	if mimeType == "" {
		mimeType = "image/png" // 默认 PNG
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("image_type", "message")

	// 手动构造带正确 Content-Type 的 form part（CreateFormFile 写死 application/octet-stream）
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="%s"`, filepath.Base(filePath)))
	h.Set("Content-Type", mimeType)
	fw, err := w.CreatePart(h)
	if err != nil {
		return "", fmt.Errorf("create form part: %w", err)
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", fmt.Errorf("copy image: %w", err)
	}
	w.Close()

	req, _ := http.NewRequest("POST", config.Cfg.FeishuAPIBase+"/open-apis/im/v1/images", &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload image: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ImageKey string `json:"image_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse upload image response: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("feishu api error %d: %s", result.Code, result.Msg)
	}
	return result.Data.ImageKey, nil
}

// UploadFile 将本地文件上传到飞书，返回 file_key
func UploadFile(filePath, fileName string) (string, error) {
	token, err := GetToken()
	if err != nil {
		return "", err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	if fileName == "" {
		fileName = filepath.Base(filePath)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("file_type", feishuFileType(filepath.Ext(filePath)))
	_ = w.WriteField("file_name", fileName)
	fw, err := w.CreateFormFile("file", fileName)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", fmt.Errorf("copy file: %w", err)
	}
	w.Close()

	req, _ := http.NewRequest("POST", config.Cfg.FeishuAPIBase+"/open-apis/im/v1/files", &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload file: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			FileKey string `json:"file_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse upload file response: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("feishu api error %d: %s", result.Code, result.Msg)
	}
	return result.Data.FileKey, nil
}

// SendImageTo 向指定接收方发送图片消息
func SendImageTo(receiveID, receiveIDType, imageKey string) error {
	token, err := GetToken()
	if err != nil {
		return err
	}
	content, _ := json.Marshal(map[string]string{"image_key": imageKey})
	body, _ := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   "image",
		"content":    string(content),
	})
	url := fmt.Sprintf(config.Cfg.FeishuAPIBase+"/open-apis/im/v1/messages?receive_id_type=%s", receiveIDType)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send image: %w", err)
	}
	defer resp.Body.Close()
	return parseAPIError(resp.Body)
}

// SendFileTo 向指定接收方发送文件消息
func SendFileTo(receiveID, receiveIDType, fileKey string) error {
	token, err := GetToken()
	if err != nil {
		return err
	}
	content, _ := json.Marshal(map[string]string{"file_key": fileKey})
	body, _ := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   "file",
		"content":    string(content),
	})
	url := fmt.Sprintf(config.Cfg.FeishuAPIBase+"/open-apis/im/v1/messages?receive_id_type=%s", receiveIDType)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send file: %w", err)
	}
	defer resp.Body.Close()
	return parseAPIError(resp.Body)
}

// ── 文件下载 ───────────────────────────────────────────────────────────────────

// DownloadResource 下载飞书消息中的图片/文件到本地，返回相对于 uploadDir 的路径
func DownloadResource(messageID, fileKey, resourceType, uploadDir string) (string, error) {
	token, err := GetToken()
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf(config.Cfg.FeishuAPIBase+"/open-apis/im/v1/messages/%s/resources/%s?type=%s",
		messageID, fileKey, resourceType)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download resource: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download resource status %d", resp.StatusCode)
	}

	ext := ".bin"
	switch resp.Header.Get("Content-Type") {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	}

	// 用 fileKey 后 8 位 + 时间戳命名，放到 uploadDir/feishu/ 子目录
	name := fileKey
	if len(name) > 8 {
		name = name[len(name)-8:]
	}
	subDir := fmt.Sprintf("%s/feishu", uploadDir)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	fileName := fmt.Sprintf("%s/feishu_%s_%d%s", subDir, name, time.Now().UnixMilli(), ext)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read resource: %w", err)
	}
	if err := os.WriteFile(fileName, data, 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return fileName, nil
}

// ── 内部工具 ──────────────────────────────────────────────────────────────────

func parseAPIError(r io.Reader) error {
	data, _ := io.ReadAll(r)
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	if result.Code != 0 {
		return fmt.Errorf("feishu api error %d: %s", result.Code, result.Msg)
	}
	return nil
}
