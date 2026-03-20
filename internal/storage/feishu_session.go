// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/storage/feishu_session.go — 飞书会话管理：按 (ownerUserID, feishuPeer) 查找或创建会话
package storage

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// FindFeishuSession 按 (ownerUserID, feishuPeer) 查找最近一次飞书会话，不存在时返回 ""（不创建）。
func FindFeishuSession(ownerUserID, feishuPeer string) (string, error) {
	if ownerUserID == "" || feishuPeer == "" {
		return "", nil
	}
	var sess Session
	err := DB.Where("user_id = ? AND feishu_peer = ? AND source = 'feishu'", ownerUserID, feishuPeer).
		Order("updated_at DESC").
		First(&sess).Error
	if err == nil {
		return sess.SessionID, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return "", fmt.Errorf("query feishu session: %w", err)
}

// GetOrCreateFeishuSession 按 (ownerUserID, feishuPeer) 查找最近一次飞书会话，
// 若不存在则创建新会话并返回 session_id。
// ownerUserID：机器人归属用户（Bot 的配置者）
// feishuPeer：飞书对话方 ID（单聊 = open_id，群聊 = chat_id）
func GetOrCreateFeishuSession(ownerUserID, feishuPeer string) (string, error) {
	if ownerUserID == "" || feishuPeer == "" {
		return "", fmt.Errorf("ownerUserID and feishuPeer are required")
	}

	var sess Session
	err := DB.Where("user_id = ? AND feishu_peer = ? AND source = 'feishu'", ownerUserID, feishuPeer).
		Order("updated_at DESC").
		First(&sess).Error
	if err == nil {
		return sess.SessionID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("query feishu session: %w", err)
	}

	// 创建新会话
	newSess := &Session{
		SessionID:  uuid.New().String(),
		UserID:     ownerUserID,
		KVData:     "{}",
		Source:     "feishu",
		FeishuPeer: feishuPeer,
	}
	if err := DB.Create(newSess).Error; err != nil {
		return "", fmt.Errorf("create feishu session: %w", err)
	}
	return newSess.SessionID, nil
}
