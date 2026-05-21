// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/storage/session_scorer.go — Langfuse Task Unit 评估的 DB 操作层
package storage

import (
	"time"
)

// TryLockScoringSession 原子抢占评估锁：将 langfuse_scoring_at 设为 now。
// 仅在 langfuse_scoring_at IS NULL 或已超过 5 分钟（进程崩溃兜底）时才成功。
// 返回 true 表示抢占成功，调用方可以开始评估；返回 false 表示其他协程正在处理。
// 注意：使用原生 SQL 以绕过 GORM autoUpdateTime，避免 updated_at 被刷新导致会话列表排序乱跳。
func TryLockScoringSession(sessionID string, now time.Time) bool {
	staleThreshold := now.Add(-5 * time.Minute)
	result := DB.Exec(
		`UPDATE sessions SET langfuse_scoring_at = ?
		 WHERE session_id = ? AND (langfuse_scoring_at IS NULL OR langfuse_scoring_at < ?)`,
		now, sessionID, staleThreshold,
	)
	return result.RowsAffected > 0
}

// UnlockScoringSession 释放评估锁（将 langfuse_scoring_at 清空）。
// 注意：使用原生 SQL 以绕过 GORM autoUpdateTime。
func UnlockScoringSession(sessionID string) {
	DB.Exec("UPDATE sessions SET langfuse_scoring_at = NULL WHERE session_id = ?", sessionID)
}

// UpdateLangfuseCursor 推进游标到 newCursorMsgID，同时清除评估锁。
// 注意：使用原生 SQL 以绕过 GORM autoUpdateTime。
func UpdateLangfuseCursor(sessionID string, newCursorMsgID uint) error {
	return DB.Exec(
		"UPDATE sessions SET langfuse_cursor_msg_id = ?, langfuse_scoring_at = NULL WHERE session_id = ?",
		newCursorMsgID, sessionID,
	).Error
}

// CountOriginMsgsAfterCursor 统计 session 中游标之后的 origin 消息数量。
// 用于条件 B 检查是否达到积压阈值。
func CountOriginMsgsAfterCursor(sessionID string) (int64, error) {
	var s Session
	if err := DB.Select("langfuse_cursor_msg_id").
		Where("session_id = ?", sessionID).First(&s).Error; err != nil {
		return 0, err
	}
	var count int64
	err := DB.Model(&OriginSessionMessage{}).
		Where("session_id = ? AND id > ?", sessionID, s.LangfuseCursorMsgID).
		Count(&count).Error
	return count, err
}

// IdleSessionForScoring 描述一条待评估的 idle session
type IdleSessionForScoring struct {
	SessionID string
	UserID    string
}

// GetIdleSessionsWithPendingScore 查找满足条件 A 的会话列表：
//   - 最后一条 origin 消息写入时间早于 idleCutoff（静默超时）
//   - 存在游标之后的消息（有内容待评估）
//   - 当前未被锁定（langfuse_scoring_at IS NULL）
//   - 非子 agent 会话
func GetIdleSessionsWithPendingScore(idleCutoff time.Time) ([]IdleSessionForScoring, error) {
	var sessions []Session
	err := DB.Select("session_id, user_id, langfuse_cursor_msg_id").
		Where(`last_origin_msg_at < ?
			AND last_origin_msg_at > ?
			AND langfuse_scoring_at IS NULL
			AND is_subagent = false`,
			idleCutoff,
			time.Time{}, // last_origin_msg_at 不为零值（有过消息）
		).
		Find(&sessions).Error
	if err != nil {
		return nil, err
	}

	result := make([]IdleSessionForScoring, 0, len(sessions))
	for _, s := range sessions {
		// 检查是否有游标之后的消息
		var count int64
		if err2 := DB.Model(&OriginSessionMessage{}).
			Where("session_id = ? AND id > ?", s.SessionID, s.LangfuseCursorMsgID).
			Count(&count).Error; err2 != nil || count == 0 {
			continue
		}
		result = append(result, IdleSessionForScoring{
			SessionID: s.SessionID,
			UserID:    s.UserID,
		})
	}
	return result, nil
}

// GetOriginMsgsAfterCursor 获取 session 中游标之后的所有 origin 消息，按 ID 升序。
func GetOriginMsgsAfterCursor(sessionID string, cursorMsgID uint) ([]OriginSessionMessage, error) {
	var msgs []OriginSessionMessage
	err := DB.Where("session_id = ? AND id > ?", sessionID, cursorMsgID).
		Order("id ASC").
		Find(&msgs).Error
	return msgs, err
}

// GetSessionCursorMsgID 获取 session 当前的评估游标 ID。
func GetSessionCursorMsgID(sessionID string) (uint, error) {
	var s Session
	if err := DB.Select("langfuse_cursor_msg_id").
		Where("session_id = ?", sessionID).First(&s).Error; err != nil {
		return 0, err
	}
	return s.LangfuseCursorMsgID, nil
}
