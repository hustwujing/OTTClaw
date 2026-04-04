// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/storage/feishu_session.go — 飞书会话管理：按 (ownerUserID, feishuPeer) 查找或创建会话
package storage

import (
	"errors"
	"fmt"

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
// 委托给通用的 GetOrCreateChannelSession 实现。
func GetOrCreateFeishuSession(ownerUserID, feishuPeer string) (string, error) {
	return GetOrCreateChannelSession("feishu", ownerUserID, feishuPeer)
}
