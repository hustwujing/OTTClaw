// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/feishu/adapter.go — 飞书渠道 Adapter 实现
package feishu

import (
	"context"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"OTTClaw/internal/channel"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

// FeishuAdapter 实现 channel.Adapter 接口
type FeishuAdapter struct{}

// Name 返回渠道名称
func (a *FeishuAdapter) Name() string { return "feishu" }

// GetConfiguredUserIDs 返回所有已配置飞书机器人的用户 ID 列表
func (a *FeishuAdapter) GetConfiguredUserIDs() ([]string, error) {
	return storage.GetAllConfiguredUsers()
}

// Connect 建立飞书长连接并阻塞直到 ctx 取消或出错。
// 收到消息时通过 dispatch 分发给框架（框架负责 session 查找和 agent 调用）。
func (a *FeishuAdapter) Connect(ctx context.Context, ownerUserID string, dispatch channel.DispatchFunc) error {
	cfg, err := storage.GetFeishuConfig(ownerUserID)
	if err != nil || cfg == nil || cfg.AppID == "" {
		logger.Warn("feishu", ownerUserID, "", "no feishu config, skip connect", 0)
		return nil
	}
	appSecret, err := storage.GetDecryptedAppSecret(ownerUserID)
	if err != nil || appSecret == "" {
		logger.Warn("feishu", ownerUserID, "", "missing app secret, skip connect", 0)
		return nil
	}
	appID := cfg.AppID

	SetCredentials(appID, appSecret)

	// doDispatch 封装 dispatch 调用，注入飞书 appID 到 ctx，并构造 writerFactory
	doDispatch := func(ctx context.Context, peer, receiveIDType, text string) {
		enrichedCtx := withAppIDCtx(ctx, appID)
		wf := func(sessID string) channel.StreamWriter {
			return newFeishuWriter(peer, receiveIDType, ownerUserID, sessID, appID)
		}
		dispatch(enrichedCtx, peer, text, wf)
	}

	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(makeAdapterMessageHandler(ctx, ownerUserID, appID, doDispatch)).
		OnP2CardActionTrigger(makeAdapterCardActionHandler(ctx, ownerUserID, appID, doDispatch))

	logger.Info("feishu", ownerUserID, "", "starting ws client appID="+appID, 0)
	wsClient := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithAutoReconnect(true),
	)
	if err := wsClient.Start(ctx); err != nil {
		if ctx.Err() == nil {
			return err
		}
	}
	return nil
}

// makeAdapterMessageHandler 是消息事件处理器，委托给包内 makeMessageHandler 逻辑，
// 但用 doDispatch 替代旧版 runAgent 调用。
func makeAdapterMessageHandler(
	botCtx context.Context,
	ownerUserID, appID string,
	doDispatch func(ctx context.Context, peer, receiveIDType, text string),
) func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	return makeMessageHandler(botCtx, ownerUserID, appID, doDispatch)
}

// makeAdapterCardActionHandler 是卡片按钮回调处理器
func makeAdapterCardActionHandler(
	botCtx context.Context,
	ownerUserID, appID string,
	doDispatch func(ctx context.Context, peer, receiveIDType, text string),
) func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	return makeCardActionHandler(botCtx, ownerUserID, appID, doDispatch)
}
