// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/storage/wecom_session.go — 企业微信 AI 机器人会话管理
// 复用 sessions 表的 feishu_peer 列存储企微对话方 ID，以 source='wecom' 区分。
// 委托给通用的 GetOrCreateChannelSession 实现。
package storage

// GetOrCreateWeComSession 按 (ownerUserID, wecomPeer) 查找最近一次企微会话，
// 若不存在则创建新会话并返回 session_id。
// 委托给通用的 GetOrCreateChannelSession 实现。
func GetOrCreateWeComSession(ownerUserID, wecomPeer string) (string, error) {
	return GetOrCreateChannelSession("wecom", ownerUserID, wecomPeer)
}
