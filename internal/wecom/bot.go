// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/wecom/bot.go — 企业微信 AI 机器人 WebSocket 客户端
//
// 协议（@wecom/aibot-node-sdk 定义）：
//   - 连接：wss://openws.work.weixin.qq.com（默认）
//   - 认证：{ cmd:"aibot_subscribe", headers:{req_id}, body:{bot_id,secret} }
//   - 心跳：{ cmd:"ping", headers:{req_id} }
//   - 收消息：{ cmd:"aibot_msg_callback", headers:{req_id}, body:{...} }
//   - 收事件：{ cmd:"aibot_event_callback", headers:{req_id}, body:{...} }
//   - 回复：{ cmd:"aibot_respond_msg", headers:{req_id}, body:{msgtype:"stream", stream:{id,seq,finish,content}} }
package wecom

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"OTTClaw/config"
	"OTTClaw/internal/logger"
)

const (
	MediaChunkSize = 512 * 1024 // 512KB per chunk (before base64)
	maxChunks      = 100
)

// ── 协议类型 ──────────────────────────────────────────────────────────────────

// WsFrame 是所有帧的通用结构
type WsFrame struct {
	Cmd     string            `json:"cmd,omitempty"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body,omitempty"`
	ErrCode int               `json:"errcode,omitempty"`
	ErrMsg  string            `json:"errmsg,omitempty"`
}

// MsgBody 是 aibot_msg_callback 的 body
type MsgBody struct {
	MsgID    string          `json:"msgid"`
	AiBotID  string          `json:"aibotid"`
	ChatID   string          `json:"chatid"`   // 群聊时有值
	ChatType string          `json:"chattype"` // "single" | "group"
	From     struct {
		UserID string `json:"userid"`
	} `json:"from"`
	MsgType string          `json:"msgtype"`
	Text    *struct {
		Content string `json:"content"`
	} `json:"text,omitempty"`
	Voice *struct {
		Content string `json:"content"` // 语音转文字
	} `json:"voice,omitempty"`
	Image *struct {
		URL    string `json:"url,omitempty"`
		AESKey string `json:"aeskey,omitempty"`
	} `json:"image,omitempty"`
	File *struct {
		URL    string `json:"url,omitempty"`
		AESKey string `json:"aeskey,omitempty"`
	} `json:"file,omitempty"`
}

// ── 客户端 ────────────────────────────────────────────────────────────────────

// MessageHandler 收到有效消息帧时的回调（异步，不得阻塞）
type MessageHandler func(reqID string, msg *MsgBody)

// BotClient 持有单个用户的企微 AI 机器人 WebSocket 长连接
type BotClient struct {
	ownerUserID string
	botID       string
	secret      string
	onMessage   MessageHandler

	mu            sync.Mutex
	conn          *websocket.Conn
	connectedAt   time.Time
	authenticated bool

	// 序列号（用于生成 req_id）
	seq atomic.Uint64

	// 心跳
	heartbeatTimer *time.Timer
	missedPongs    int

	// 重连
	reconnectAttempts int

	// 请求-响应匹配（用于媒体上传等同步操作）
	pendingMu        sync.Mutex
	pendingResponses map[string]chan map[string]any
}

// NewBotClient 创建一个新的 BotClient（未连接）
func NewBotClient(ownerUserID, botID, secret string, onMessage MessageHandler) *BotClient {
	return &BotClient{
		ownerUserID:      ownerUserID,
		botID:            botID,
		secret:           secret,
		onMessage:        onMessage,
		pendingResponses: make(map[string]chan map[string]any),
	}
}

// genReqID 生成唯一请求 ID
func (c *BotClient) genReqID() string {
	return uuid.New().String()
}

// sendFrame 序列化并发送一帧（调用方需持有 mu 或保证 conn 非 nil）
func (c *BotClient) sendFrame(frame any) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

// sendAuth 发送认证帧
func (c *BotClient) sendAuth() error {
	return c.sendFrame(map[string]any{
		"cmd":     "aibot_subscribe",
		"headers": map[string]string{"req_id": c.genReqID()},
		"body":    map[string]string{"bot_id": c.botID, "secret": c.secret},
	})
}

// sendAndWait 发送一帧，阻塞等待匹配 req_id 的响应或超时
func (c *BotClient) sendAndWait(frame any, reqID string, timeout time.Duration) (map[string]any, error) {
	ch := make(chan map[string]any, 1)
	c.pendingMu.Lock()
	c.pendingResponses[reqID] = ch
	c.pendingMu.Unlock()

	if err := c.sendFrame(frame); err != nil {
		c.pendingMu.Lock()
		delete(c.pendingResponses, reqID)
		c.pendingMu.Unlock()
		return nil, err
	}

	select {
	case resp := <-ch:
		if code, _ := resp["errcode"].(float64); code != 0 {
			msg, _ := resp["errmsg"].(string)
			return resp, fmt.Errorf("aibot error %v: %s", code, msg)
		}
		return resp, nil
	case <-time.After(timeout):
		c.pendingMu.Lock()
		delete(c.pendingResponses, reqID)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for req_id %s", reqID)
	}
}

// UploadMedia 将本地文件分块上传到 AI Bot，返回 media_id
func (c *BotClient) UploadMedia(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	size := len(data)
	numChunks := (size + MediaChunkSize - 1) / MediaChunkSize
	if numChunks > maxChunks {
		return "", fmt.Errorf("file too large: %d chunks > max %d (%.1fMB)",
			numChunks, maxChunks, float64(size)/(1024*1024))
	}

	sum := md5.Sum(data)
	md5Hex := fmt.Sprintf("%x", sum)
	filename := filepath.Base(filePath)

	// Step 1: init
	initReqID := c.genReqID()
	initResp, err := c.sendAndWait(map[string]any{
		"cmd":     "aibot_upload_media_init",
		"headers": map[string]string{"req_id": initReqID},
		"body": map[string]any{
			"filename":     filename,
			"size":         size,
			"md5":          md5Hex,
			"chunks_count": numChunks,
		},
	}, initReqID, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("upload init: %w", err)
	}
	uploadID, _ := initResp["upload_id"].(string)
	if uploadID == "" {
		return "", fmt.Errorf("upload init: no upload_id in response")
	}

	// Step 2: chunks
	for i := 0; i < numChunks; i++ {
		start := i * MediaChunkSize
		end := start + MediaChunkSize
		if end > size {
			end = size
		}
		chunk := data[start:end]

		chunkReqID := c.genReqID()
		_, err := c.sendAndWait(map[string]any{
			"cmd":     "aibot_upload_media_chunk",
			"headers": map[string]string{"req_id": chunkReqID},
			"body": map[string]any{
				"upload_id":   uploadID,
				"chunk_index": i,
				"data":        base64.StdEncoding.EncodeToString(chunk),
			},
		}, chunkReqID, 30*time.Second)
		if err != nil {
			return "", fmt.Errorf("upload chunk %d/%d: %w", i+1, numChunks, err)
		}
	}

	// Step 3: finish
	finishReqID := c.genReqID()
	finishResp, err := c.sendAndWait(map[string]any{
		"cmd":     "aibot_upload_media_finish",
		"headers": map[string]string{"req_id": finishReqID},
		"body":    map[string]any{"upload_id": uploadID},
	}, finishReqID, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("upload finish: %w", err)
	}
	mediaID, _ := finishResp["media_id"].(string)
	if mediaID == "" {
		return "", fmt.Errorf("upload finish: no media_id in response")
	}
	return mediaID, nil
}

// SendMediaReply 向用户发送图片或文件消息（mediaType 为 "image" 或 "file"）
func (c *BotClient) SendMediaReply(reqID, mediaID, mediaType string) error {
	return c.sendFrame(map[string]any{
		"cmd":     "aibot_respond_msg",
		"headers": map[string]string{"req_id": reqID},
		"body": map[string]any{
			"msgtype": mediaType,
			mediaType: map[string]string{"media_id": mediaID},
		},
	})
}

// SendReply 向指定 req_id 的对话发送流式回复（finish=true 时为最终帧）
func (c *BotClient) SendReply(reqID, streamID string, seq int, content string, finish bool) error {
	return c.sendFrame(map[string]any{
		"cmd":     "aibot_respond_msg",
		"headers": map[string]string{"req_id": reqID},
		"body": map[string]any{
			"msgtype": "stream",
			"stream": map[string]any{
				"id":      streamID,
				"seq":     seq,
				"finish":  finish,
				"content": content,
			},
		},
	})
}

// scheduleHeartbeat 启动（重置）心跳定时器
func (c *BotClient) scheduleHeartbeat() {
	interval := time.Duration(config.Cfg.WeComBotHeartbeatSec) * time.Second
	c.mu.Lock()
	if c.heartbeatTimer != nil {
		c.heartbeatTimer.Reset(interval)
	} else {
		c.heartbeatTimer = time.AfterFunc(interval, c.doHeartbeat)
	}
	c.mu.Unlock()
}

// doHeartbeat 发送 ping 帧
func (c *BotClient) doHeartbeat() {
	c.mu.Lock()
	conn := c.conn
	missed := c.missedPongs
	c.mu.Unlock()
	if conn == nil {
		return
	}
	if missed >= 3 {
		logger.Warn("wecom", c.ownerUserID, "", "missed 3 pongs, closing connection", 0)
		_ = conn.Close()
		return
	}
	if err := c.sendFrame(map[string]any{
		"cmd":     "ping",
		"headers": map[string]string{"req_id": c.genReqID()},
	}); err != nil {
		logger.Warn("wecom", c.ownerUserID, "", fmt.Sprintf("heartbeat send error: %v", err), 0)
	}
	c.mu.Lock()
	c.missedPongs++
	c.mu.Unlock()
	c.scheduleHeartbeat()
}

// Run 建立连接、认证、循环读帧，直到 ctx 取消或超过最大重连次数。
func (c *BotClient) Run(ctx context.Context) {
	maxRetry := config.Cfg.WeComBotMaxReconnect
	for {
		if ctx.Err() != nil {
			return
		}
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.Warn("wecom", c.ownerUserID, "", fmt.Sprintf("connection closed: %v", err), 0)
		}
		c.reconnectAttempts++
		if maxRetry > 0 && c.reconnectAttempts >= maxRetry {
			logger.Warn("wecom", c.ownerUserID, "",
				fmt.Sprintf("max reconnect attempts (%d) reached, giving up", maxRetry), 0)
			return
		}
		// 指数退避：1s, 2s, 4s, ... 上限 30s
		exp := min(c.reconnectAttempts-1, 5)
		if exp < 0 {
			exp = 0
		}
		delaySecs := 1 << uint(exp)
		if delaySecs > 30 {
			delaySecs = 30
		}
		delay := time.Duration(delaySecs) * time.Second
		logger.Info("wecom", c.ownerUserID, "",
			fmt.Sprintf("reconnecting in %s (attempt %d/%d)", delay, c.reconnectAttempts, maxRetry), 0)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
	}
}

// runOnce 建立一次 WebSocket 连接，阻塞直到断开
func (c *BotClient) runOnce(ctx context.Context) error {
	wsURL := config.Cfg.WeComBotWSURL
	dialer := websocket.Dialer{}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsURL, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.authenticated = false
	c.missedPongs = 0
	c.connectedAt = time.Now()
	c.mu.Unlock()

	logger.Info("wecom", c.ownerUserID, "", fmt.Sprintf("connected to %s, sending auth", wsURL), 0)

	if err := c.sendAuth(); err != nil {
		_ = conn.Close()
		return fmt.Errorf("send auth: %w", err)
	}

	// 关闭时清理
	defer func() {
		c.mu.Lock()
		if c.heartbeatTimer != nil {
			c.heartbeatTimer.Stop()
			c.heartbeatTimer = nil
		}
		c.conn = nil
		c.authenticated = false
		c.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		if ctx.Err() != nil {
			return nil
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		c.handleRaw(raw)
	}
}

// handleRaw 解析并分发一帧
func (c *BotClient) handleRaw(raw []byte) {
	var frame WsFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		logger.Warn("wecom", c.ownerUserID, "", fmt.Sprintf("unmarshal frame: %v", err), 0)
		return
	}

	switch frame.Cmd {
	case "": // 认证/心跳/上传响应（无 cmd）
		// 优先检查是否有等待此 req_id 的调用（媒体上传等同步操作）
		if reqID := frame.Headers["req_id"]; reqID != "" {
			c.pendingMu.Lock()
			ch, ok := c.pendingResponses[reqID]
			if ok {
				delete(c.pendingResponses, reqID)
			}
			c.pendingMu.Unlock()
			if ok {
				resp := map[string]any{
					"errcode": frame.ErrCode,
					"errmsg":  frame.ErrMsg,
				}
				if frame.Body != nil {
					var body map[string]any
					if err := json.Unmarshal(frame.Body, &body); err == nil {
						for k, v := range body {
							resp[k] = v
						}
					}
				}
				ch <- resp
				return
			}
		}
		if frame.ErrCode != 0 {
			logger.Warn("wecom", c.ownerUserID, "",
				fmt.Sprintf("ack error %d: %s", frame.ErrCode, frame.ErrMsg), 0)
			return
		}
		c.mu.Lock()
		wasAuth := c.authenticated
		c.authenticated = true
		c.missedPongs = 0
		c.mu.Unlock()
		if !wasAuth {
			// 首次认证成功，启动心跳
			c.reconnectAttempts = 0
			logger.Info("wecom", c.ownerUserID, "", "authenticated, heartbeat started", 0)
			c.scheduleHeartbeat()
		}

	case "aibot_msg_callback":
		if c.onMessage == nil || frame.Body == nil {
			return
		}
		var msg MsgBody
		if err := json.Unmarshal(frame.Body, &msg); err != nil {
			logger.Warn("wecom", c.ownerUserID, "", fmt.Sprintf("unmarshal msg body: %v", err), 0)
			return
		}
		reqID := frame.Headers["req_id"]
		go c.onMessage(reqID, &msg) // 异步，不阻塞读循环

	case "aibot_event_callback":
		// 暂时只记录日志，后续可扩展（进入会话欢迎语等）
		logger.Info("wecom", c.ownerUserID, "", fmt.Sprintf("event callback: %s", string(frame.Body)), 0)

	default:
		logger.Info("wecom", c.ownerUserID, "", fmt.Sprintf("unhandled cmd: %s", frame.Cmd), 0)
	}
}

// min 辅助：取两个整数的较小值（Go 1.21 内置 min，低版本需要此辅助）
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
