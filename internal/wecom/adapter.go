// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/wecom/adapter.go — 企业微信渠道 Adapter 实现
package wecom

import (
	"context"
	"strings"

	"OTTClaw/internal/channel"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

// WeComAdapter 实现 channel.Adapter 接口
type WeComAdapter struct{}

// Name 返回渠道名称
func (a *WeComAdapter) Name() string { return "wecom" }

// GetConfiguredUserIDs 返回所有已配置企微 AI 机器人凭证的用户 ID 列表
func (a *WeComAdapter) GetConfiguredUserIDs() ([]string, error) {
	return storage.GetAllWeComBotConfiguredUsers()
}

// Connect 建立企微 AI 机器人长连接并阻塞直到 ctx 取消或出错。
// 收到消息时通过 dispatch 分发给框架。
func (a *WeComAdapter) Connect(ctx context.Context, ownerUserID string, dispatch channel.DispatchFunc) error {
	botID, err := storage.GetWeComBotConfig(ownerUserID)
	if err != nil || botID == "" {
		logger.Warn("wecom", ownerUserID, "", "missing bot_id, skip connect", 0)
		return nil
	}
	secret, err := storage.GetDecryptedWeComSecret(ownerUserID)
	if err != nil || secret == "" {
		logger.Warn("wecom", ownerUserID, "", "missing/decrypt secret, skip connect", 0)
		return nil
	}

	// client 先声明，onMessage 闭包通过变量引用捕获，NewBotClient 赋值后闭包内可见
	var client *BotClient

	// onMessage 是企微消息回调，解析文本后调用 dispatch
	onMessage := func(reqID string, msg *MsgBody) {
		peer := msg.From.UserID
		if msg.ChatType == "group" && msg.ChatID != "" {
			peer = msg.ChatID
		}
		if peer == "" {
			return
		}

		var userText string
		switch msg.MsgType {
		case "text":
			if msg.Text != nil {
				userText = strings.TrimSpace(msg.Text.Content)
			}
		case "voice":
			if msg.Voice != nil {
				userText = strings.TrimSpace(msg.Voice.Content)
			}
		case "image":
			userText = "[用户发送了图片]"
		case "file":
			userText = "[用户发送了文件]"
		default:
			return
		}
		if userText == "" {
			return
		}

		// writerFactory 闭包捕获已认证的 client（变量引用，调用时已赋值）
		wf := func(sessID string) channel.StreamWriter {
			return newWeComWriter(ownerUserID, sessID, reqID, client)
		}
		dispatch(ctx, peer, userText, wf)
	}

	client = NewBotClient(ownerUserID, botID, secret, onMessage)
	logger.Info("wecom", ownerUserID, "", "bot started (botID="+botID+")", 0)
	client.Run(ctx)
	return nil
}
