// internal/weixin/client.go — 微信 ilink bot 长轮询客户端
package weixin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"OTTClaw/config"
	"OTTClaw/internal/logger"
)

const (
	TextChunkLimit    = 4000
	SessionExpiredErr = -14
	MaxConsecFail     = 3
	BackoffDelay      = 30 * time.Second
	RetryDelay        = 2 * time.Second
	QRLoginTimeout    = 480 * time.Second
	QRMaxRefreshes    = 10
)

// ErrSessionExpired is returned by pollLoop when the server rejects the token (-14).
var ErrSessionExpired = errors.New("session expired")

type MessageHandler func(fromUserID, contextToken, text string)

type Client struct {
	ownerUserID string
	api         *API
	onMessage   MessageHandler

	mu            sync.Mutex
	contextTokens map[string]string
	getUpdatesBuf string
	receivedMsgs  map[string]time.Time

	LoginStatus  string
	CurrentQRURL string
	QRImgContent string
	BindError    string // 绑定流程中发生的错误（如账号已被其他用户绑定）
}

func NewClient(ownerUserID string, onMessage MessageHandler) *Client {
	return &Client{
		ownerUserID:   ownerUserID,
		onMessage:     onMessage,
		contextTokens: make(map[string]string),
		receivedMsgs:  make(map[string]time.Time),
		LoginStatus:   "idle",
	}
}

// GetKnownSenders 返回已缓存 context_token 的所有 sender ID（即曾向本 bot 发过消息的用户）
func (c *Client) GetKnownSenders() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, 0, len(c.contextTokens))
	for id := range c.contextTokens {
		ids = append(ids, id)
	}
	return ids
}

func (c *Client) Run(ctx context.Context, baseURL, token, cdnBaseURL string) error {
	if token != "" {
		c.api = NewAPI(baseURL, token, cdnBaseURL)
		c.LoginStatus = "logged_in"
		logger.Info("weixin", c.ownerUserID, "", "connected with saved token", 0)
		return c.pollLoop(ctx)
	}
	result, err := c.QRLogin(ctx, baseURL)
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("QR login cancelled")
	}
	newBaseURL, _ := result["base_url"].(string)
	if newBaseURL == "" {
		newBaseURL = baseURL
	}
	newToken, _ := result["token"].(string)
	c.api = NewAPI(newBaseURL, newToken, cdnBaseURL)
	c.LoginStatus = "logged_in"
	return c.pollLoop(ctx)
}

func (c *Client) GetAPI() *API { return c.api }

// ── QR Login ────────────────────────────────────────────────

func (c *Client) QRLogin(ctx context.Context, baseURL string) (map[string]any, error) {
	api := NewAPI(baseURL, "", "")
	c.LoginStatus = "waiting_scan"
	qrResp, err := api.FetchQRCode()
	if err != nil {
		c.LoginStatus = "idle"
		return nil, fmt.Errorf("fetch QR code: %w", err)
	}
	qrcode, _ := qrResp["qrcode"].(string)
	qrcodeURL, _ := qrResp["qrcode_img_content"].(string)
	if qrcode == "" {
		c.LoginStatus = "idle"
		return nil, fmt.Errorf("no QR code returned")
	}
	c.mu.Lock()
	c.CurrentQRURL = qrcodeURL
	c.QRImgContent = qrcodeURL
	c.mu.Unlock()
	logger.Info("weixin", c.ownerUserID, "", "QR code ready: "+qrcodeURL, 0)

	scannedPrinted := false
	refreshCount := 0
	deadline := time.Now().Add(QRLoginTimeout)
	for {
		select {
		case <-ctx.Done():
			c.LoginStatus = "idle"
			c.CurrentQRURL = ""
			return nil, ctx.Err()
		default:
		}
		if time.Now().After(deadline) {
			c.LoginStatus = "idle"
			c.CurrentQRURL = ""
			return nil, fmt.Errorf("QR login timed out")
		}
		statusResp, err := api.PollQRStatus(qrcode)
		if err != nil {
			c.LoginStatus = "idle"
			return nil, fmt.Errorf("QR poll error: %w", err)
		}
		status, _ := statusResp["status"].(string)
		switch status {
		case "wait":
		case "scaned":
			c.LoginStatus = "scanned"
			if !scannedPrinted {
				logger.Info("weixin", c.ownerUserID, "", "QR scanned, waiting confirm", 0)
				scannedPrinted = true
			}
		case "expired":
			refreshCount++
			if refreshCount >= QRMaxRefreshes {
				c.LoginStatus = "idle"
				c.CurrentQRURL = ""
				return nil, fmt.Errorf("QR refreshed %d times, giving up", QRMaxRefreshes)
			}
			logger.Info("weixin", c.ownerUserID, "", fmt.Sprintf("QR expired, refreshing (%d/%d)", refreshCount, QRMaxRefreshes), 0)
			qrResp, err = api.FetchQRCode()
			if err != nil {
				return nil, fmt.Errorf("QR refresh: %w", err)
			}
			qrcode, _ = qrResp["qrcode"].(string)
			qrcodeURL, _ = qrResp["qrcode_img_content"].(string)
			scannedPrinted = false
			c.mu.Lock()
			c.CurrentQRURL = qrcodeURL
			c.QRImgContent = qrcodeURL
			c.mu.Unlock()
		case "confirmed":
			botToken, _ := statusResp["bot_token"].(string)
			botID, _ := statusResp["ilink_bot_id"].(string)
			resultBaseURL, _ := statusResp["baseurl"].(string)
			ilinkUserID, _ := statusResp["ilink_user_id"].(string)
			logger.Info("weixin", c.ownerUserID, "", fmt.Sprintf("login confirmed: bot_id=%s ilink_user_id=%s baseurl=%s token_len=%d", botID, ilinkUserID, resultBaseURL, len(botToken)), 0)
			if resultBaseURL == "" {
				resultBaseURL = baseURL
			}
			if botToken == "" || botID == "" {
				return nil, fmt.Errorf("login confirmed but missing token/bot_id")
			}
			c.CurrentQRURL = ""
			c.QRImgContent = ""
			return map[string]any{"token": botToken, "base_url": resultBaseURL, "bot_id": botID, "ilink_user_id": ilinkUserID}, nil
		}
		time.Sleep(1 * time.Second)
	}
}

// ── Long-poll loop ──────────────────────────────────────────

func (c *Client) pollLoop(ctx context.Context) error {
	logger.Info("weixin", c.ownerUserID, "", fmt.Sprintf("starting long-poll loop, ctx.Err=%v", ctx.Err()), 0)
	consecFail := 0
	for {
		select {
		case <-ctx.Done():
			logger.Info("weixin", c.ownerUserID, "", "pollLoop: ctx done, exiting", 0)
			return nil
		default:
		}
		logger.Info("weixin", c.ownerUserID, "", "calling getUpdates...", 0)
		resp, err := c.api.GetUpdates(c.getUpdatesBuf)
		if err == nil {
			msgs, _ := resp["msgs"].([]any)
			ret := jsonInt(resp, "ret")
			buf, _ := resp["get_updates_buf"].(string)
			logger.Info("weixin", c.ownerUserID, "", fmt.Sprintf("getUpdates: ret=%d msgs=%d buf_len=%d", ret, len(msgs), len(buf)), 0)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			consecFail++
			logger.Warn("weixin", c.ownerUserID, "", fmt.Sprintf("getUpdates error: %v (%d/%d)", err, consecFail, MaxConsecFail), 0)
			if consecFail >= MaxConsecFail {
				consecFail = 0
				sleepCtx(ctx, BackoffDelay)
			} else {
				sleepCtx(ctx, RetryDelay)
			}
			continue
		}
		ret := jsonInt(resp, "ret")
		errcode := jsonInt(resp, "errcode")
		if ret != 0 || errcode != 0 {
			if errcode == SessionExpiredErr || ret == SessionExpiredErr {
				logger.Warn("weixin", c.ownerUserID, "", "session expired (-14)", 0)
				return ErrSessionExpired
			}
			consecFail++
			errmsg, _ := resp["errmsg"].(string)
			logger.Warn("weixin", c.ownerUserID, "", fmt.Sprintf("getUpdates ret=%d errcode=%d errmsg=%s", ret, errcode, errmsg), 0)
			if consecFail >= MaxConsecFail {
				consecFail = 0
				sleepCtx(ctx, BackoffDelay)
			} else {
				sleepCtx(ctx, RetryDelay)
			}
			continue
		}
		consecFail = 0
		if buf, ok := resp["get_updates_buf"].(string); ok && buf != "" {
			c.getUpdatesBuf = buf
		}
		msgs, _ := resp["msgs"].([]any)
		for _, m := range msgs {
			raw, ok := m.(map[string]any)
			if !ok {
				continue
			}
			c.processMessage(raw)
		}
	}
}

func (c *Client) processMessage(raw map[string]any) {
	msgID := jsonStr(raw, "message_id")
	if msgID == "" {
		msgID = jsonStr(raw, "seq")
	}
	if msgID != "" {
		c.mu.Lock()
		if _, dup := c.receivedMsgs[msgID]; dup {
			c.mu.Unlock()
			return
		}
		c.receivedMsgs[msgID] = time.Now()
		cutoff := time.Now().Add(-7 * time.Hour)
		for k, t := range c.receivedMsgs {
			if t.Before(cutoff) {
				delete(c.receivedMsgs, k)
			}
		}
		c.mu.Unlock()
	}

	fromUser := jsonStr(raw, "from_user_id")
	contextToken := jsonStr(raw, "context_token")
	if contextToken != "" && fromUser != "" {
		c.mu.Lock()
		c.contextTokens[fromUser] = contextToken
		c.mu.Unlock()
	}
	itemList, _ := raw["item_list"].([]any)
	var textParts []string
	for _, item := range itemList {
		it, ok := item.(map[string]any)
		if !ok {
			continue
		}
		itype := jsonInt(it, "type")
		switch itype {
		case 1: // 文本
			textItem, _ := it["text_item"].(map[string]any)
			if textItem != nil {
				if t, _ := textItem["text"].(string); t != "" {
					textParts = append(textParts, t)
				}
			}
		case 2: // 图片
			if path := c.downloadMedia(it, "image_item", ".jpg"); path != "" {
				textParts = append(textParts, fmt.Sprintf("[文件: %s]", path))
			} else {
				textParts = append(textParts, "[用户发送了图片]")
			}
		case 3: // 语音
			voiceItem, _ := it["voice_item"].(map[string]any)
			if voiceItem != nil {
				if vt, ok := voiceItem["text"].(string); ok && vt != "" {
					textParts = append(textParts, vt)
				}
			}
		case 4: // 文件
			fileItem, _ := it["file_item"].(map[string]any)
			fileName := ""
			if fileItem != nil {
				fileName, _ = fileItem["file_name"].(string)
			}
			ext := filepath.Ext(fileName)
			if ext == "" {
				ext = ".bin"
			}
			if path := c.downloadMedia(it, "file_item", ext); path != "" {
				textParts = append(textParts, fmt.Sprintf("[文件: %s]", path))
			} else {
				textParts = append(textParts, "[用户发送了文件]")
			}
		case 5: // 视频
			if path := c.downloadMedia(it, "video_item", ".mp4"); path != "" {
				textParts = append(textParts, fmt.Sprintf("[文件: %s]", path))
			} else {
				textParts = append(textParts, "[用户发送了视频]")
			}
		}
	}
	text := strings.TrimSpace(strings.Join(textParts, "\n"))
	if text == "" {
		return
	}
	logger.Info("weixin", c.ownerUserID, "", fmt.Sprintf("msg from=%s text=%s", fromUser, truncate(text, 50)), 0)
	if c.onMessage != nil {
		c.onMessage(fromUser, contextToken, text)
	}
}

// ── Media download ──────────────────────────────────────────

// downloadMedia 从 CDN 下载媒体文件到本地，返回本地路径；失败返回空字符串。
// itemKey 为 "image_item"/"file_item"/"video_item"，ext 为默认扩展名。
func (c *Client) downloadMedia(item map[string]any, itemKey, defaultExt string) string {
	info, _ := item[itemKey].(map[string]any)
	if info == nil {
		return ""
	}
	media, _ := info["media"].(map[string]any)
	if media == nil {
		return ""
	}
	encryptParam, _ := media["encrypt_query_param"].(string)
	aesKey, _ := info["aeskey"].(string)
	if aesKey == "" {
		aesKey, _ = media["aes_key"].(string)
	}
	if encryptParam == "" || aesKey == "" {
		logger.Warn("weixin", c.ownerUserID, "", fmt.Sprintf("missing CDN params for %s", itemKey), 0)
		return ""
	}

	cdnBase := DefaultCDNBaseURL
	if c.api != nil {
		cdnBase = c.api.CDNBaseURL
	}
	savePath := filepath.Join(config.Cfg.UploadDir, fmt.Sprintf("wx_%d%s", time.Now().UnixNano(), defaultExt))
	if err := DownloadMediaFromCDN(cdnBase, encryptParam, aesKey, savePath); err != nil {
		logger.Warn("weixin", c.ownerUserID, "", fmt.Sprintf("download %s failed: %v", itemKey, err), 0)
		return ""
	}
	logger.Info("weixin", c.ownerUserID, "", fmt.Sprintf("media downloaded: %s", savePath), 0)
	return savePath
}

// ── Send helpers ────────────────────────────────────────────

// resolveContextToken 返回 to 用户的 context_token：
// 优先用传入值，其次查缓存，最后尝试 getconfig 动态获取。
func (c *Client) resolveContextToken(to, contextToken string) (string, error) {
	if contextToken != "" {
		return contextToken, nil
	}
	c.mu.Lock()
	contextToken = c.contextTokens[to]
	c.mu.Unlock()
	if contextToken != "" {
		return contextToken, nil
	}
	// 无缓存时尝试 getconfig（适用于主动发起会话）
	resp, err := c.api.GetConfig(to, "")
	if err == nil {
		if ct, ok := resp["context_token"].(string); ok && ct != "" {
			c.mu.Lock()
			c.contextTokens[to] = ct
			c.mu.Unlock()
			logger.Info("weixin", c.ownerUserID, "", fmt.Sprintf("getconfig context_token acquired for %s", to), 0)
			return ct, nil
		}
	}
	return "", fmt.Errorf("无法获取 context_token：对方（%s）尚未向本账号发过消息，请让对方先发一条消息再试", to)
}

func (c *Client) SendText(to, text, contextToken string) error {
	if c.api == nil {
		return fmt.Errorf("微信未连接")
	}
	ct, err := c.resolveContextToken(to, contextToken)
	if err != nil {
		logger.Warn("weixin", c.ownerUserID, "", err.Error(), 0)
		return err
	}
	chunks := splitText(text, TextChunkLimit)
	for i, chunk := range chunks {
		if err := c.api.SendText(to, chunk, ct); err != nil {
			logger.Warn("weixin", c.ownerUserID, "", fmt.Sprintf("send text chunk %d/%d error: %v", i+1, len(chunks), err), 0)
			return err
		}
		if i < len(chunks)-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil
}

func (c *Client) SendImage(to, imagePath, contextToken string) error {
	if c.api == nil {
		return fmt.Errorf("微信未连接")
	}
	ct, err := c.resolveContextToken(to, contextToken)
	if err != nil {
		return err
	}
	localPath, err := resolveMediaPath(imagePath)
	if err != nil {
		return err
	}
	result, err := UploadMediaToCDN(c.api, localPath, to, 1)
	if err != nil {
		return err
	}
	return c.api.SendImageItem(to, ct, result.EncryptQueryParam, result.AESKeyB64, result.CiphertextSize)
}

func (c *Client) SendFile(to, filePath, contextToken string) error {
	if c.api == nil {
		return fmt.Errorf("微信未连接")
	}
	ct, err := c.resolveContextToken(to, contextToken)
	if err != nil {
		return err
	}
	localPath, err := resolveMediaPath(filePath)
	if err != nil {
		return err
	}
	result, err := UploadMediaToCDN(c.api, localPath, to, 3)
	if err != nil {
		return err
	}
	return c.api.SendFileItem(to, ct, result.EncryptQueryParam, result.AESKeyB64, filepath.Base(localPath), result.RawSize)
}

// ── Utilities ───────────────────────────────────────────────

func splitText(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		if len(text) <= limit {
			chunks = append(chunks, text)
			break
		}
		cut := strings.LastIndex(text[:limit], "\n\n")
		if cut <= 0 {
			cut = strings.LastIndex(text[:limit], "\n")
		}
		if cut <= 0 {
			cut = limit
		}
		chunks = append(chunks, text[:cut])
		text = strings.TrimLeft(text[cut:], "\n")
	}
	return chunks
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func jsonInt(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case int:
		return n
	}
	return 0
}

func jsonStr(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

