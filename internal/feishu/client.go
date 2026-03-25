// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/feishu/client.go — 飞书长连接客户端、消息处理、feishuWriter
package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"OTTClaw/config"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

// mdImageRe 匹配 Markdown 图片语法 ![alt](url)。
// 飞书 lark_md 不渲染该语法，会直接显示为原始文本；
// 图片已通过 WriteImage 单独以原生图片消息发出，文字里只需移除这类标记。
var mdImageRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)

// StreamWriter 与 agent.StreamWriter 接口相同，避免循环依赖
type StreamWriter interface {
	WriteText(text string) error
	WriteProgress(step, detail string, elapsedMs int64) error
	WriteInteractive(kind string, data any) error
	WriteSpeaker(name string) error
	WriteImage(url string) error
	WriteEnd() error
	WriteError(msg string) error
}

// AgentRunFunc agent 执行函数类型，由外部（main.go）注入以避免循环依赖
type AgentRunFunc func(ctx context.Context, userID, sessionID, userText string, writer StreamWriter) error

var agentRunner AgentRunFunc

// SetAgentRunner 注入 agent 执行函数（服务启动后、feishu 使用前调用）
func SetAgentRunner(fn AgentRunFunc) {
	agentRunner = fn
}

// ── Context key：注入飞书 appID，供 tool/feishu.go 无需额外存储查询时直接获取 ──

type appIDCtxKey struct{}

// AppIDFromCtx 从 context 中取出飞书 appID（飞书发起的请求会注入；Web 请求返回空串）
func AppIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(appIDCtxKey{}).(string)
	return v
}

func withAppIDCtx(ctx context.Context, appID string) context.Context {
	return context.WithValue(ctx, appIDCtxKey{}, appID)
}

// ── Registry：管理每个用户的长连接客户端 ─────────────────────────────────────

type registry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc // userID → cancel
}

// Registry 全局单例
var Registry = &registry{
	cancels: make(map[string]context.CancelFunc),
}

// StartAll 在服务启动时为所有已配置飞书机器人的用户启动长连接
func (r *registry) StartAll(ctx context.Context) {
	userIDs, err := storage.GetAllConfiguredUsers()
	if err != nil {
		logger.Warn("feishu", "", "", fmt.Sprintf("get configured users: %v", err), 0)
		return
	}
	for _, uid := range userIDs {
		cfg, err := storage.GetFeishuConfig(uid)
		if err != nil || cfg == nil {
			continue
		}
		r.StartForUser(ctx, uid, cfg)
	}
}

// StartForUser 为指定用户启动（或重启）飞书长连接
func (r *registry) StartForUser(ctx context.Context, ownerUserID string, cfg *storage.FeishuConfig) {
	r.mu.Lock()
	if cancel, ok := r.cancels[ownerUserID]; ok {
		cancel()
	}
	ctx2, cancel := context.WithCancel(ctx)
	r.cancels[ownerUserID] = cancel
	r.mu.Unlock()

	appSecret, err := storage.GetDecryptedAppSecret(ownerUserID)
	if err != nil || appSecret == "" {
		logger.Warn("feishu", ownerUserID, "", "missing app secret, skip start", 0)
		cancel()
		return
	}

	go r.run(ctx2, ownerUserID, cfg.AppID, appSecret)
}

// StopForUser 停止指定用户的飞书长连接
func (r *registry) StopForUser(ownerUserID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancel, ok := r.cancels[ownerUserID]; ok {
		cancel()
		delete(r.cancels, ownerUserID)
	}
}

// StopAll 停止所有飞书长连接（服务关闭时调用）
func (r *registry) StopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for uid, cancel := range r.cancels {
		cancel()
		delete(r.cancels, uid)
	}
}

// run 实际启动长连接（阻塞；ctx 取消时退出）
func (r *registry) run(ctx context.Context, ownerUserID, appID, appSecret string) {
	logger.Info("feishu", ownerUserID, "", fmt.Sprintf("starting ws client appID=%s", appID), 0)

	SetCredentials(appID, appSecret)

	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(makeMessageHandler(ctx, ownerUserID, appID)).
		OnP2CardActionTrigger(makeCardActionHandler(ctx, ownerUserID, appID))

	wsClient := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithAutoReconnect(true),
	)

	if err := wsClient.Start(ctx); err != nil {
		if ctx.Err() == nil {
			logger.Warn("feishu", ownerUserID, "", fmt.Sprintf("ws client error: %v", err), 0)
		}
	}
	logger.Info("feishu", ownerUserID, "", "ws client stopped", 0)
}

// ── 消息事件处理器 ─────────────────────────────────────────────────────────────

func makeMessageHandler(botCtx context.Context, ownerUserID, appID string) func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	return func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		if event.Event == nil || event.Event.Message == nil || event.Event.Sender == nil {
			return nil
		}
		msg := event.Event.Message
		sender := event.Event.Sender

		openID := ""
		if sender.SenderId != nil && sender.SenderId.OpenId != nil {
			openID = *sender.SenderId.OpenId
		}

		chatID := ""
		if msg.ChatId != nil {
			chatID = *msg.ChatId
		}
		chatType := "p2p"
		if msg.ChatType != nil {
			chatType = *msg.ChatType
		}
		msgType := ""
		if msg.MessageType != nil {
			msgType = *msg.MessageType
		}
		content := ""
		if msg.Content != nil {
			content = *msg.Content
		}
		messageID := ""
		if msg.MessageId != nil {
			messageID = *msg.MessageId
		}

		// 确定对话方 ID 和接收类型
		peer := openID
		receiveIDType := "open_id"
		if chatType == "group" {
			peer = chatID
			receiveIDType = "chat_id"
		}
		if peer == "" {
			return nil
		}

		// 异步处理，不阻塞事件循环（飞书要求 3s 内返回）
		go func() {
			// 幂等保护：飞书在服务超时/网络抖动时会重发相同 message_id 的事件，直接丢弃重复投递
			if isDuplicateMessage(messageID) {
				logger.Info("feishu", ownerUserID, "", fmt.Sprintf("dedup: skipping duplicate message_id=%s", messageID), 0)
				return
			}

			sessionID, err := storage.GetOrCreateFeishuSession(ownerUserID, peer)
			if err != nil {
				logger.Error("feishu", ownerUserID, "", "get session", err, 0)
				return
			}

			// 检查是否在等待特定类型的回复
			if kind, ok := PopPending(sessionID); ok {
				handleSpecialReply(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, msgType, content, messageID, kind)
				return
			}

			switch msgType {
			case "text", "post":
				text := extractText(msgType, content)
				if text != "" {
					runAgent(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, text)
				}
			case "image":
				var imgContent struct {
					ImageKey string `json:"image_key"`
				}
				if err := json.Unmarshal([]byte(content), &imgContent); err != nil || imgContent.ImageKey == "" {
					return
				}
				path, err := DownloadResource(appID, messageID, imgContent.ImageKey, "image", config.Cfg.UploadDir)
				if err != nil {
					logger.Warn("feishu", ownerUserID, sessionID, fmt.Sprintf("download image: %v", err), 0)
					return
				}
				runAgent(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, fmt.Sprintf("[文件: %s]", path))
			case "file", "audio", "video":
				var fileContent struct {
					FileKey  string `json:"file_key"`
					FileName string `json:"file_name"`
				}
				if err := json.Unmarshal([]byte(content), &fileContent); err != nil || fileContent.FileKey == "" {
					return
				}
				path, err := DownloadResource(appID, messageID, fileContent.FileKey, "file", config.Cfg.UploadDir)
				if err != nil {
					logger.Warn("feishu", ownerUserID, sessionID, fmt.Sprintf("download %s: %v", msgType, err), 0)
					return
				}
				userMsg := fmt.Sprintf("[文件: %s]", path)
				if fileContent.FileName != "" {
					userMsg += "\n文件名: " + fileContent.FileName
				}
				runAgent(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, userMsg)
			default:
				if msgType != "" {
					_ = SendTextTo(appID, peer, receiveIDType, "暂不支持 "+msgType+" 类型的消息，请发送文字、图片、音频或文件。")
				}
			}
		}()

		return nil
	}
}

// handleSpecialReply 处理处于等待状态时收到的回复（如图片/文件上传、选项确认）
func handleSpecialReply(botCtx context.Context, ownerUserID, sessionID, peer, receiveIDType, appID, msgType, content, messageID string, kind pendingKind) {
	switch kind {
	case PendingChoice:
		// 用户以文字回复了选项或确认，直接透传给 agent
		text := extractText(msgType, content)
		if text == "" {
			text = content
		}
		runAgent(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, text)

	case PendingUpload:
		if msgType == "image" {
			var imgContent struct {
				ImageKey string `json:"image_key"`
			}
			if err := json.Unmarshal([]byte(content), &imgContent); err == nil && imgContent.ImageKey != "" {
				path, err := DownloadResource(appID, messageID, imgContent.ImageKey, "image", config.Cfg.UploadDir)
				if err != nil {
					logger.Warn("feishu", ownerUserID, sessionID, fmt.Sprintf("download image: %v", err), 0)
					runAgent(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, "图片上传失败，请重试")
					return
				}
				runAgent(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, fmt.Sprintf("[文件: %s]", path))
				return
			}
		}
		if msgType == "file" || msgType == "audio" || msgType == "video" {
			var fileContent struct {
				FileKey  string `json:"file_key"`
				FileName string `json:"file_name"`
			}
			if err := json.Unmarshal([]byte(content), &fileContent); err == nil && fileContent.FileKey != "" {
				path, err := DownloadResource(appID, messageID, fileContent.FileKey, "file", config.Cfg.UploadDir)
				if err != nil {
					logger.Warn("feishu", ownerUserID, sessionID, fmt.Sprintf("download %s: %v", msgType, err), 0)
					runAgent(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, "文件上传失败，请重试")
					return
				}
				userMsg := fmt.Sprintf("[文件: %s]", path)
				if fileContent.FileName != "" {
					userMsg += "\n文件名: " + fileContent.FileName
				}
				runAgent(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, userMsg)
				return
			}
		}
		// 用户发了非图片/文件内容或文字"skip"，视为跳过
		text := extractText(msgType, content)
		if text == "" {
			text = "skip"
		}
		runAgent(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, text)
	}
}

// extractText 从消息内容 JSON 中提取纯文本
func extractText(msgType, content string) string {
	switch msgType {
	case "text":
		var c struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(content), &c); err == nil {
			return strings.TrimSpace(c.Text)
		}
	case "post":
		// 富文本：遍历 zh_cn 所有段落，拼接 text 节点
		var c struct {
			ZhCn struct {
				Content [][]struct {
					Tag  string `json:"tag"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"zh_cn"`
		}
		if err := json.Unmarshal([]byte(content), &c); err == nil {
			var sb strings.Builder
			for _, para := range c.ZhCn.Content {
				for _, seg := range para {
					if seg.Tag == "text" {
						sb.WriteString(seg.Text)
					}
				}
				sb.WriteString("\n")
			}
			return strings.TrimSpace(sb.String())
		}
	}
	return ""
}

// runAgent 启动 agent 处理并将结果写回飞书
// ctx 为 botCtx（服务关闭时取消），appID 注入到 agentCtx 供工具层使用
func runAgent(ctx context.Context, ownerUserID, sessionID, peer, receiveIDType, appID, userText string) {
	w := newFeishuWriter(peer, receiveIDType, ownerUserID, sessionID, appID)
	defer w.close()

	if agentRunner == nil {
		_ = w.WriteError("agent not initialized")
		return
	}

	agentCtx := withAppIDCtx(ctx, appID)
	if err := agentRunner(agentCtx, ownerUserID, sessionID, userText, w); err != nil {
		logger.Error("feishu", ownerUserID, sessionID, "agent run", err, 0)
	}
}

// ── feishuWriter：实现 agent.StreamWriter，先回复等待提示，完成后一次性发送 ────

// spinnerFrames ⏳/⌛ 交替形成"沙漏翻转"动画
var spinnerFrames = []string{
	"⏳ 消息收到，正在处理…",
	"⌛ 消息收到，正在处理…",
}

type feishuWriter struct {
	peer          string
	receiveIDType string
	ownerUserID   string
	sessionID     string
	appID         string // 发送方 Bot 的 appID，用于 API 鉴权

	mu            sync.Mutex
	textBuf       strings.Builder
	ackMsgID      string             // 等待提示卡片的 message_id，用于最终 UpdateCard
	finalized     bool               // 防止重复发送
	spinnerCancel context.CancelFunc // 停止动画帧刷新
}

func newFeishuWriter(peer, receiveIDType, ownerUserID, sessionID, appID string) *feishuWriter {
	w := &feishuWriter{
		peer:          peer,
		receiveIDType: receiveIDType,
		ownerUserID:   ownerUserID,
		sessionID:     sessionID,
		appID:         appID,
	}
	// 立即发送等待提示（交互卡片），让用户知道消息已收到，后续可 PATCH 更新为最终回复
	id, err := SendCardGetID(appID, peer, receiveIDType, spinnerFrames[0])
	if err != nil {
		logger.Warn("feishu", ownerUserID, sessionID, fmt.Sprintf("send ack: %v", err), 0)
	} else {
		w.ackMsgID = id
		ctx, cancel := context.WithCancel(context.Background())
		w.spinnerCancel = cancel
		go w.runSpinner(ctx)
	}
	return w
}

// runSpinner 定期切换沙漏帧，直到 ctx 被取消或 finalized
func (w *feishuWriter) runSpinner(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(config.Cfg.FeishuSpinnerIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	frame := 1
	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			done := w.finalized
			ackMsgID := w.ackMsgID
			w.mu.Unlock()
			if done || ackMsgID == "" {
				return
			}
			_ = UpdateCard(w.appID, ackMsgID, spinnerFrames[frame%len(spinnerFrames)])
			frame++
		case <-ctx.Done():
			return
		}
	}
}

// sendFinal 用最终文本更新/发送消息（幂等，只执行一次）
// 先设 finalized 标志阻止 spinner 继续写卡片，再执行最终更新。
// 若 UpdateCard 失败，记录日志并降级为发送新消息。
func (w *feishuWriter) sendFinal(text string) {
	w.mu.Lock()
	if w.finalized {
		w.mu.Unlock()
		return
	}
	w.finalized = true
	ackMsgID := w.ackMsgID
	spinnerCancel := w.spinnerCancel
	w.mu.Unlock()

	// 先停止动画（finalized 已为 true，spinner 不会再写卡片）
	if spinnerCancel != nil {
		spinnerCancel()
	}

	// 飞书 lark_md 不支持 ![alt](url) 图片语法，直接显示为原始文本。
	// 图片已通过 WriteImage 单独以原生图片消息发送，这里将残留的图片 markdown 移除。
	text = strings.TrimSpace(mdImageRe.ReplaceAllString(text, ""))
	if text == "" {
		text = "✅ 已完成"
	}

	if ackMsgID != "" {
		if err := UpdateCard(w.appID, ackMsgID, text); err != nil {
			logger.Warn("feishu", w.ownerUserID, w.sessionID,
				fmt.Sprintf("update card failed (%v), falling back to new message", err), 0)
			_ = SendTextTo(w.appID, w.peer, w.receiveIDType, text)
		}
	} else {
		if err := SendTextTo(w.appID, w.peer, w.receiveIDType, text); err != nil {
			logger.Warn("feishu", w.ownerUserID, w.sessionID,
				fmt.Sprintf("send final message failed: %v", err), 0)
		}
	}
}

// close 是 defer 安全网：若 WriteEnd/WriteError/WriteInteractive 已完成则忽略
func (w *feishuWriter) close() {
	w.mu.Lock()
	text := w.textBuf.String()
	w.mu.Unlock()
	if text == "" {
		text = "✅ 已完成"
	}
	w.sendFinal(text)
}

// WriteText 累积 LLM 文字 chunk
func (w *feishuWriter) WriteText(text string) error {
	w.mu.Lock()
	w.textBuf.WriteString(text)
	w.mu.Unlock()
	return nil
}

// WriteProgress 飞书无 UI 进度条，忽略
func (w *feishuWriter) WriteProgress(_, _ string, _ int64) error { return nil }

// WriteSpeaker 飞书无动态发言人，忽略
func (w *feishuWriter) WriteSpeaker(_ string) error { return nil }

// WriteEnd 将完整响应一次性发出
func (w *feishuWriter) WriteEnd() error {
	w.mu.Lock()
	text := w.textBuf.String()
	w.mu.Unlock()
	if text == "" {
		text = "✅ 已完成"
	}
	w.sendFinal(text)
	return nil
}

// WriteImage 上传图片到飞书并以图片消息发出
func (w *feishuWriter) WriteImage(url string) error {
	// url 形如 /output/3/xxx.png，转为本地相对路径后取绝对路径
	localPath, err := filepath.Abs(strings.TrimPrefix(url, "/"))
	if err != nil {
		return err
	}
	imageKey, err := UploadImage(w.appID, localPath)
	if err != nil {
		return fmt.Errorf("feishu upload image: %w", err)
	}
	return SendImageTo(w.appID, w.peer, w.receiveIDType, imageKey)
}

// WriteError 向飞书发送错误提示
func (w *feishuWriter) WriteError(msg string) error {
	w.sendFinal("❌ " + msg)
	return nil
}

// WriteInteractive 处理需要用户交互的结构化事件
// 调用前先将已积累的文字刷出，再发送交互卡片
func (w *feishuWriter) WriteInteractive(kind string, data any) error {
	// 先将已积累的文字更新到 ack 消息
	w.mu.Lock()
	text := w.textBuf.String()
	w.textBuf.Reset()
	ackMsgID := w.ackMsgID
	w.ackMsgID = "" // 后续 close() 若再执行，需要新发消息
	spinnerCancel := w.spinnerCancel
	if !w.finalized {
		w.finalized = true // 由本函数负责最终输出，阻止 close() 重复发送
	}
	w.mu.Unlock()

	// 停止动画（finalized 已为 true）
	if spinnerCancel != nil {
		spinnerCancel()
	}

	// 与 sendFinal 保持一致：移除飞书不支持的 ![alt](url) 语法
	text = strings.TrimSpace(mdImageRe.ReplaceAllString(text, ""))
	if text != "" {
		if ackMsgID != "" {
			if err := UpdateCard(w.appID, ackMsgID, text); err != nil {
				logger.Warn("feishu", w.ownerUserID, w.sessionID,
					fmt.Sprintf("update card (interactive prefix) failed (%v), sending new message", err), 0)
				_ = SendTextTo(w.appID, w.peer, w.receiveIDType, text)
			}
		} else {
			_ = SendTextTo(w.appID, w.peer, w.receiveIDType, text)
		}
	} else if ackMsgID != "" {
		// 没有文字前缀，将等待提示更新为空白占位，避免残留"正在处理"
		if err := UpdateCard(w.appID, ackMsgID, "👇"); err != nil {
			logger.Warn("feishu", w.ownerUserID, w.sessionID,
				fmt.Sprintf("update ack placeholder failed: %v", err), 0)
		}
	}

	switch kind {
	case "options":
		type optItem struct {
			Label string `json:"label"`
			Value string `json:"value"`
		}
		type optPayload struct {
			Title   string    `json:"title"`
			Options []optItem `json:"options"`
		}
		b, _ := json.Marshal(data)
		var payload optPayload
		if err := json.Unmarshal(b, &payload); err != nil {
			return err
		}
		var sb strings.Builder
		sb.WriteString(payload.Title)
		sb.WriteString("\n\n请回复选项编号：\n")
		for i, o := range payload.Options {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, o.Label))
		}
		MarkPending(w.sessionID, PendingChoice)
		return SendTextTo(w.appID, w.peer, w.receiveIDType, strings.TrimRight(sb.String(), "\n"))

	case "confirm":
		type confirmPayload struct {
			Message      string `json:"message"`
			ConfirmLabel string `json:"confirm_label"`
			CancelLabel  string `json:"cancel_label"`
		}
		b, _ := json.Marshal(data)
		var payload confirmPayload
		if err := json.Unmarshal(b, &payload); err != nil {
			return err
		}
		confirmLabel := payload.ConfirmLabel
		if confirmLabel == "" {
			confirmLabel = "确认"
		}
		cancelLabel := payload.CancelLabel
		if cancelLabel == "" {
			cancelLabel = "取消"
		}
		msg := fmt.Sprintf("%s\n\n请回复「%s」或「%s」", payload.Message, confirmLabel, cancelLabel)
		MarkPending(w.sessionID, PendingChoice)
		return SendTextTo(w.appID, w.peer, w.receiveIDType, msg)

	case "file_upload":
		type uploadPayload struct {
			Title  string `json:"title"`
			Prompt string `json:"prompt"`
		}
		b, _ := json.Marshal(data)
		var payload uploadPayload
		_ = json.Unmarshal(b, &payload)
		hint := payload.Title
		if payload.Prompt != "" {
			hint += "\n" + payload.Prompt
		}
		hint += "\n（直接发送图片，或回复 skip 跳过）"
		MarkPending(w.sessionID, PendingUpload)
		return SendTextTo(w.appID, w.peer, w.receiveIDType, hint)
	}

	return nil
}

// ── 卡片按钮回调处理器 ─────────────────────────────────────────────────────────

// makeCardActionHandler 处理飞书交互卡片的按钮点击事件（card.action.trigger）。
// 未注册此处理器时，飞书会向用户返回"稍后再试"（错误码 200340）。
// 处理流程：
//  1. 提取按钮值中的 __choice__ 字段作为用户选择文本
//  2. 优先以 openID（单聊场景）查找会话；若无则以 chatID（群聊场景）查找/创建
//  3. 异步调用 runAgent，向当前会话注入用户选择
//  4. 立即返回 toast 确认，防止飞书超时报错
func makeCardActionHandler(botCtx context.Context, ownerUserID, appID string) func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	return func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		if event.Event == nil {
			return &callback.CardActionTriggerResponse{}, nil
		}

		// 提取点击者 openID 和会话 chatID
		openID := event.Event.Operator.OpenID
		chatID := ""
		if event.Event.Context != nil {
			chatID = event.Event.Context.OpenChatID
		}

		// 从按钮 value 中提取 __choice__ 字段
		choiceText := ""
		if event.Event.Action != nil {
			if v, ok := event.Event.Action.Value["__choice__"]; ok {
				choiceText, _ = v.(string)
			}
		}
		if choiceText == "" || openID == "" {
			return &callback.CardActionTriggerResponse{}, nil
		}

		// 确定 peer 与 receiveIDType：优先单聊（openID），其次群聊（chatID）
		peer := ""
		receiveIDType := ""
		if openID != "" {
			sessID, err := storage.FindFeishuSession(ownerUserID, openID)
			if err == nil && sessID != "" {
				peer = openID
				receiveIDType = "open_id"
			}
		}
		if peer == "" && chatID != "" {
			peer = chatID
			receiveIDType = "chat_id"
		}
		if peer == "" {
			// 兜底：使用 openID 作为 peer（会创建新会话）
			peer = openID
			receiveIDType = "open_id"
		}

		go func() {
			sessionID, err := storage.GetOrCreateFeishuSession(ownerUserID, peer)
			if err != nil {
				logger.Error("feishu", ownerUserID, "", "card action get session", err, 0)
				return
			}
			logger.Info("feishu", ownerUserID, sessionID,
				fmt.Sprintf("card action: peer=%s choice=%q", peer, choiceText), 0)
			runAgent(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, choiceText)
		}()

		// 立即返回 toast，告知飞书已受理，避免超时报错
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{
				Type:    "info",
				Content: "处理中…",
			},
		}, nil
	}
}

// 编译期检查：feishuWriter 必须实现本包定义的 StreamWriter 接口
var _ StreamWriter = (*feishuWriter)(nil)
