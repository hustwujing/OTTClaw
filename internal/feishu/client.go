// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/feishu/client.go — 飞书消息处理、feishuWriter、RunForSession
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

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"OTTClaw/config"
	"OTTClaw/internal/channel"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

// mdImageRe 匹配 Markdown 图片语法 ![alt](url)。
// 飞书 lark_md 不渲染该语法，会直接显示为原始文本；
// 图片已通过 WriteImage 单独以原生图片消息发出，文字里只需移除这类标记。
var mdImageRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)

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

// ── Registry 引用：供 RunForSession 使用 ────────────────────────────────────────

// activeRegistry 由 SetRegistry 注入，RunForSession 通过它将消息路由给 agent
var activeRegistry *channel.Registry

// SetRegistry 注入 channel.Registry（服务启动时，在 StartAll 之前调用）
func SetRegistry(reg *channel.Registry) {
	activeRegistry = reg
}

// GetRegistry 返回当前的 channel.Registry（nil 表示未初始化）
func GetRegistry() *channel.Registry {
	return activeRegistry
}

// ── 消息事件处理器 ─────────────────────────────────────────────────────────────

// makeMessageHandler 创建飞书消息事件处理器。
// doDispatch：接收 (ctx, peer, receiveIDType, text)，转发给 channel 框架
func makeMessageHandler(
	botCtx context.Context,
	ownerUserID, appID string,
	doDispatch func(ctx context.Context, peer, receiveIDType, text string),
) func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
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
			if messageID != "" && isDuplicateMessage(messageID) {
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
				handleSpecialReply(botCtx, ownerUserID, sessionID, peer, receiveIDType, appID, msgType, content, messageID, kind, doDispatch)
				return
			}

			switch msgType {
			case "text", "post":
				text := extractText(msgType, content)
				if text != "" {
					doDispatch(botCtx, peer, receiveIDType, text)
				}
			case "image":
				var imgContent struct {
					ImageKey string `json:"image_key"`
				}
				if err := json.Unmarshal([]byte(content), &imgContent); err != nil || imgContent.ImageKey == "" {
					return
				}
				path, err := DownloadResource(appID, messageID, imgContent.ImageKey, "image", config.Cfg.UploadDir, "")
				if err != nil {
					logger.Warn("feishu", ownerUserID, sessionID, fmt.Sprintf("download image: %v", err), 0)
					return
				}
				doDispatch(botCtx, peer, receiveIDType, fmt.Sprintf("[文件: %s]", path))
			case "file", "audio", "video":
				var fileContent struct {
					FileKey  string `json:"file_key"`
					FileName string `json:"file_name"`
				}
				if err := json.Unmarshal([]byte(content), &fileContent); err != nil || fileContent.FileKey == "" {
					return
				}
				path, err := DownloadResource(appID, messageID, fileContent.FileKey, "file", config.Cfg.UploadDir, fileContent.FileName)
				if err != nil {
					logger.Warn("feishu", ownerUserID, sessionID, fmt.Sprintf("download %s: %v", msgType, err), 0)
					return
				}
				userMsg := fmt.Sprintf("[文件: %s]", path)
				if fileContent.FileName != "" {
					userMsg += "\n文件名: " + fileContent.FileName
				}
				doDispatch(botCtx, peer, receiveIDType, userMsg)
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
func handleSpecialReply(botCtx context.Context, ownerUserID, sessionID, peer, receiveIDType, appID, msgType, content, messageID string, kind pendingKind, doDispatch func(ctx context.Context, peer, receiveIDType, text string)) {
	switch kind {
	case PendingChoice:
		text := extractText(msgType, content)
		if text == "" {
			text = content
		}
		doDispatch(botCtx, peer, receiveIDType, text)

	case PendingUpload:
		if msgType == "image" {
			var imgContent struct {
				ImageKey string `json:"image_key"`
			}
			if err := json.Unmarshal([]byte(content), &imgContent); err == nil && imgContent.ImageKey != "" {
				path, err := DownloadResource(appID, messageID, imgContent.ImageKey, "image", config.Cfg.UploadDir, "")
				if err != nil {
					logger.Warn("feishu", ownerUserID, sessionID, fmt.Sprintf("download image: %v", err), 0)
					doDispatch(botCtx, peer, receiveIDType, "图片上传失败，请重试")
					return
				}
				doDispatch(botCtx, peer, receiveIDType, fmt.Sprintf("[文件: %s]", path))
				return
			}
		}
		if msgType == "file" || msgType == "audio" || msgType == "video" {
			var fileContent struct {
				FileKey  string `json:"file_key"`
				FileName string `json:"file_name"`
			}
			if err := json.Unmarshal([]byte(content), &fileContent); err == nil && fileContent.FileKey != "" {
				path, err := DownloadResource(appID, messageID, fileContent.FileKey, "file", config.Cfg.UploadDir, fileContent.FileName)
				if err != nil {
					logger.Warn("feishu", ownerUserID, sessionID, fmt.Sprintf("download %s: %v", msgType, err), 0)
					doDispatch(botCtx, peer, receiveIDType, "文件上传失败，请重试")
					return
				}
				userMsg := fmt.Sprintf("[文件: %s]", path)
				if fileContent.FileName != "" {
					userMsg += "\n文件名: " + fileContent.FileName
				}
				doDispatch(botCtx, peer, receiveIDType, userMsg)
				return
			}
		}
		text := extractText(msgType, content)
		if text == "" {
			text = "skip"
		}
		doDispatch(botCtx, peer, receiveIDType, text)
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

// RunForSession 在已有 session 上主动触发 agent 执行，由子 agent 完成通知调用。
// 等效于飞书收到用户文字消息时内部的调用路径，但从外部包（runner.go）触发。
func RunForSession(ctx context.Context, ownerUserID, _ /* sessionID */, peer, receiveIDType, appID, userText string) {
	if activeRegistry == nil {
		logger.Warn("feishu", ownerUserID, "", "RunForSession: activeRegistry not set", 0)
		return
	}
	enrichedCtx := withAppIDCtx(ctx, appID)
	wf := func(sessID string) channel.StreamWriter {
		return newFeishuWriter(peer, receiveIDType, ownerUserID, sessID, appID)
	}
	activeRegistry.Dispatch(enrichedCtx, ownerUserID, peer, userText, wf)
}

// ── 卡片按钮回调处理器 ─────────────────────────────────────────────────────────

// makeCardActionHandler 处理飞书交互卡片的按钮点击事件（card.action.trigger）。
func makeCardActionHandler(
	botCtx context.Context,
	ownerUserID, appID string,
	doDispatch func(ctx context.Context, peer, receiveIDType, text string),
) func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	return func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		if event.Event == nil {
			return &callback.CardActionTriggerResponse{}, nil
		}

		openID := event.Event.Operator.OpenID
		chatID := ""
		if event.Event.Context != nil {
			chatID = event.Event.Context.OpenChatID
		}

		choiceText := ""
		if event.Event.Action != nil {
			if v, ok := event.Event.Action.Value["__choice__"]; ok {
				choiceText, _ = v.(string)
			}
		}
		if choiceText == "" || openID == "" {
			return &callback.CardActionTriggerResponse{}, nil
		}

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
			peer = openID
			receiveIDType = "open_id"
		}

		go func() {
			doDispatch(botCtx, peer, receiveIDType, choiceText)
		}()

		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{
				Type:    "info",
				Content: "处理中…",
			},
		}, nil
	}
}

// ── feishuWriter：实现 channel.StreamWriter ──────────────────────────────────

// spinnerFrames ⏳/⌛ 交替形成"沙漏翻转"动画
var spinnerFrames = []string{
	"⏳ 消息收到，让我先看看，别慌，小场面…",
	"⌛ 消息收到，让我先看看，别慌，小场面…",
}

type feishuWriter struct {
	channel.BaseWriter // textBuf, finalized, WriteText, WriteEnd, WriteError, Close

	peer          string
	receiveIDType string
	appID         string

	fmu           sync.Mutex         // 保护飞书专属可变状态
	ackMsgID      string             // 等待提示卡片的 message_id，用于最终 UpdateCard
	spinnerCancel context.CancelFunc // 停止动画帧刷新

	// notifyCh：WriteText 被调用时发信号，让 runSpinner 立即刷新卡片（缓冲=1，丢弃多余信号）
	notifyCh chan struct{}
}

func newFeishuWriter(peer, receiveIDType, ownerUserID, sessionID, appID string) *feishuWriter {
	w := &feishuWriter{
		peer:          peer,
		receiveIDType: receiveIDType,
		appID:         appID,
		notifyCh:      make(chan struct{}, 1),
	}
	w.BaseWriter.OwnerUserID = ownerUserID
	w.BaseWriter.SessionID = sessionID

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

	// 注入飞书专属 SendFn：停止 spinner 并更新卡片
	w.BaseWriter.SendFn = func(text string) {
		w.fmu.Lock()
		sc := w.spinnerCancel
		ackMsgID := w.ackMsgID
		w.fmu.Unlock()

		if sc != nil {
			sc()
		}

		// 移除飞书不支持的 ![alt](url) 图片语法
		text = strings.TrimSpace(mdImageRe.ReplaceAllString(text, ""))
		if text == "" {
			text = "✅ 已完成"
		}

		if ackMsgID != "" {
			if err := UpdateCard(w.appID, ackMsgID, text); err != nil {
				logger.Warn("feishu", w.OwnerUserID, w.SessionID,
					fmt.Sprintf("update card failed (%v), falling back to new message", err), 0)
				_ = SendTextTo(w.appID, w.peer, w.receiveIDType, text)
			}
		} else {
			if err := SendTextTo(w.appID, w.peer, w.receiveIDType, text); err != nil {
				logger.Warn("feishu", w.OwnerUserID, w.SessionID,
					fmt.Sprintf("send final message failed: %v", err), 0)
			}
		}
	}

	return w
}

// WriteText 在 BaseWriter 基础上追加通知，让 runSpinner 立即刷新卡片
func (w *feishuWriter) WriteText(text string) error {
	_ = w.BaseWriter.WriteText(text)
	// 非阻塞发信号：缓冲=1，多余信号丢弃
	select {
	case w.notifyCh <- struct{}{}:
	default:
	}
	return nil
}

// runSpinner 有两个模式：
//   - 无文本时：交替显示沙漏帧（等待 LLM 响应）
//   - 有文本时：实时显示累积文本 + "▍" 光标（LLM 正在输出）
//
// 响应两路触发：notifyCh（WriteText 立即触发）和 ticker（兜底定时刷新）
func (w *feishuWriter) runSpinner(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(config.Cfg.FeishuSpinnerIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	frame := 1
	var lastDisplay string

	doUpdate := func() {
		if w.IsFinalized() {
			return
		}
		w.fmu.Lock()
		ackMsgID := w.ackMsgID
		w.fmu.Unlock()
		if ackMsgID == "" {
			return
		}

		var display string
		if text := strings.TrimSpace(mdImageRe.ReplaceAllString(w.FlushText(), "")); text != "" {
			display = text + "\n▍"
		} else {
			display = spinnerFrames[frame%len(spinnerFrames)]
			frame++
		}
		if display == lastDisplay {
			return
		}
		lastDisplay = display
		_ = UpdateCard(w.appID, ackMsgID, display)
	}

	for {
		select {
		case <-w.notifyCh:
			doUpdate()
		case <-ticker.C:
			doUpdate()
		case <-ctx.Done():
			return
		}
	}
}

// WriteProgress 飞书无 UI 进度条，忽略
func (w *feishuWriter) WriteProgress(_, _, _ string, _ int64) error { return nil }

// WriteSpeaker 飞书无动态发言人，忽略
func (w *feishuWriter) WriteSpeaker(_ string) error { return nil }

// WriteImage 上传图片到飞书并以图片消息发出
func (w *feishuWriter) WriteImage(url string) error {
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

// WriteInteractive 处理需要用户交互的结构化事件。
// 先将已积累的文字刷出，再发送交互卡片。
func (w *feishuWriter) WriteInteractive(kind string, data any) error {
	// 原子读取并清空文字缓冲
	text := w.FlushAndResetText()

	// TryFinalize 确保只有一个 WriteInteractive/WriteEnd/WriteError 能继续
	if !w.TryFinalize() {
		return nil
	}

	// 取飞书专属状态并清空（防止 SendFn 在 finalized=true 后被其他路径触发时重复发送）
	w.fmu.Lock()
	ackMsgID := w.ackMsgID
	w.ackMsgID = ""
	sc := w.spinnerCancel
	w.fmu.Unlock()

	if sc != nil {
		sc()
	}

	// 与 sendFinal 保持一致：移除飞书不支持的 ![alt](url) 语法
	text = strings.TrimSpace(mdImageRe.ReplaceAllString(text, ""))
	if text != "" {
		if ackMsgID != "" {
			if err := UpdateCard(w.appID, ackMsgID, text); err != nil {
				logger.Warn("feishu", w.OwnerUserID, w.SessionID,
					fmt.Sprintf("update card (interactive prefix) failed (%v), sending new message", err), 0)
				_ = SendTextTo(w.appID, w.peer, w.receiveIDType, text)
			}
		} else {
			_ = SendTextTo(w.appID, w.peer, w.receiveIDType, text)
		}
	} else if ackMsgID != "" {
		// 没有文字前缀，将等待提示更新为空白占位，避免残留"正在处理"
		if err := UpdateCard(w.appID, ackMsgID, "👇"); err != nil {
			logger.Warn("feishu", w.OwnerUserID, w.SessionID,
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
		MarkPending(w.SessionID, PendingChoice)
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
		MarkPending(w.SessionID, PendingChoice)
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
		MarkPending(w.SessionID, PendingUpload)
		return SendTextTo(w.appID, w.peer, w.receiveIDType, hint)
	}

	return nil
}

// 编译期检查：feishuWriter 必须实现 channel.StreamWriter 接口
var _ channel.StreamWriter = (*feishuWriter)(nil)
