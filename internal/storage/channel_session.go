// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/storage/channel_session.go — 通用渠道会话管理，消除 feishu/wecom 各自的 copy-paste
package storage

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// GetOrCreateChannelSession 按 (source, ownerUserID, peerID) 查找最近一次渠道会话，
// 若不存在则创建新会话并返回 session_id。
// source：渠道名称（"feishu" / "wecom"），对应 sessions.source 字段
// ownerUserID：机器人归属用户（Bot 的配置者）
// peerID：对话方 ID（飞书: open_id 或 chat_id；企微: userid 或 chatid）
func GetOrCreateChannelSession(source, ownerUserID, peerID string) (string, error) {
	if ownerUserID == "" || peerID == "" {
		return "", fmt.Errorf("ownerUserID and peerID are required")
	}

	var sess Session
	err := DB.Where("user_id = ? AND feishu_peer = ? AND source = ?", ownerUserID, peerID, source).
		Order("updated_at DESC").
		First(&sess).Error
	if err == nil {
		return sess.SessionID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("query %s session: %w", source, err)
	}

	newSess := &Session{
		SessionID:  uuid.New().String(),
		UserID:     ownerUserID,
		KVData:     "{}",
		Source:     source,
		FeishuPeer: peerID, // 复用 feishu_peer 列存储所有渠道的对话方 ID
	}
	if err := DB.Create(newSess).Error; err != nil {
		return "", fmt.Errorf("create %s session: %w", source, err)
	}
	return newSess.SessionID, nil
}
