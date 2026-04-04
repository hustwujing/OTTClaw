// internal/weixin/adapter.go — 微信渠道 Adapter 实现（channel.Adapter 接口）
package weixin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"OTTClaw/internal/channel"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

type WeixinAdapter struct{}

func (a *WeixinAdapter) Name() string { return "weixin" }

func (a *WeixinAdapter) GetConfiguredUserIDs() ([]string, error) {
	return storage.GetAllWeixinConfiguredUsers()
}

func (a *WeixinAdapter) Connect(ctx context.Context, ownerUserID string, dispatch channel.DispatchFunc) error {
	cfg, err := storage.GetWeixinConfig(ownerUserID)
	if err != nil || cfg == nil {
		logger.Warn("weixin", ownerUserID, "", "no weixin config, skip connect", 0)
		return nil
	}
	token, err := storage.GetDecryptedWeixinToken(ownerUserID)
	if err != nil {
		logger.Warn("weixin", ownerUserID, "", fmt.Sprintf("decrypt token error: %v", err), 0)
		token = ""
	}
	if token == "" {
		logger.Warn("weixin", ownerUserID, "", "no token stored, skipping connect (re-bind via UI to reconnect)", 0)
		return nil
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	ownerIDSaved := cfg.OwnerIlinkUserID != ""
	var client *Client
	onMessage := func(fromUserID, contextToken, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		// 首次收到消息时自动回填 owner_ilink_user_id（兼容绑定时未存储的旧数据）
		if !ownerIDSaved && fromUserID != "" {
			ownerIDSaved = true
			if err := storage.SetWeixinOwnerID(ownerUserID, fromUserID); err != nil {
				logger.Warn("weixin", ownerUserID, "", fmt.Sprintf("auto-save owner id: %v", err), 0)
			} else {
				logger.Info("weixin", ownerUserID, "", fmt.Sprintf("auto-saved owner_ilink_user_id=%s", fromUserID), 0)
			}
		}
		logger.Info("weixin", ownerUserID, "", fmt.Sprintf("dispatching msg from=%s", fromUserID), 0)
		wf := func(sessID string) channel.StreamWriter {
			return newWeixinWriter(ownerUserID, sessID, fromUserID, contextToken, client)
		}
		dispatch(ctx, fromUserID, text, wf)
	}
	client = NewClient(ownerUserID, onMessage)
	setActiveClient(ownerUserID, client)
	defer removeActiveClient(ownerUserID)
	err = client.Run(ctx, baseURL, token, DefaultCDNBaseURL)
	if errors.Is(err, ErrSessionExpired) {
		logger.Warn("weixin", ownerUserID, "",
			"session expired (-14): token rejected by server (possibly re-bound on another instance); stored token cleared, re-bind via UI to reconnect", 0)
		if clearErr := storage.ClearWeixinToken(ownerUserID); clearErr != nil {
			logger.Warn("weixin", ownerUserID, "", fmt.Sprintf("clear token: %v", clearErr), 0)
		}
	}
	return err
}
