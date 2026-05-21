// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// cmd/migrate-scorer/main.go — 一次性数据迁移工具
//
// 上线 Langfuse Task Unit 评估功能后，需要对存量 session 初始化游标字段，
// 防止历史对话被当作「待评估消息」重复触发评分。
//
// 迁移逻辑（幂等）：
//
//	对每个 langfuse_cursor_msg_id = 0 的 session，
//	找到该 session 在 origin_session_messages 中的最后一条消息，
//	将 langfuse_cursor_msg_id 和 last_origin_msg_at 更新为该消息的 id 和 created_at。
//
//	已初始化过（cursor > 0）的 session 不会被修改（幂等）。
//
// 用法：
//
//	go run cmd/migrate-scorer/main.go
//	# 或编译后
//	./migrate-scorer
//
// 脚本封装：
//
//	./scripts/migrate-scorer.sh
package main

import (
	"fmt"
	"os"
	"time"

	"OTTClaw/config"
	"OTTClaw/internal/storage"
)

func main() {
	if err := storage.InitDB(); err != nil {
		fmt.Fprintf(os.Stderr, "初始化数据库失败: %v\n", err)
		os.Exit(1)
	}

	updated, skipped, err := migrateScorer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "迁移失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("迁移完成：updated=%d  skipped(已初始化)=%d\n", updated, skipped)
}

// row 是 origin_session_messages 中每个 session 最后一条消息的摘要
type row struct {
	SessionID string
	MaxID     uint
	LastAt    time.Time
}

func migrateScorer() (updated, skipped int, err error) {
	db := storage.DB

	// 查询每个 session 的最后一条 origin 消息（max id + 对应的 created_at）
	// SQLite 和 MySQL 语法兼容
	var rows []row
	queryErr := db.Raw(`
		SELECT o.session_id, o.id AS max_id, o.created_at AS last_at
		FROM origin_session_messages o
		INNER JOIN (
			SELECT session_id, MAX(id) AS max_id
			FROM origin_session_messages
			WHERE session_id != ''
			GROUP BY session_id
		) latest ON o.session_id = latest.session_id AND o.id = latest.max_id
	`).Scan(&rows).Error
	if queryErr != nil {
		return 0, 0, fmt.Errorf("query last origin msgs: %w", queryErr)
	}

	for _, r := range rows {
		// 幂等：只更新游标为 0 的 session（未初始化）
		result := db.Exec(`
			UPDATE sessions
			SET langfuse_cursor_msg_id = ?, last_origin_msg_at = ?
			WHERE session_id = ? AND langfuse_cursor_msg_id = 0
		`, r.MaxID, r.LastAt, r.SessionID)
		if result.Error != nil {
			return updated, skipped, fmt.Errorf("update session %q: %w", r.SessionID, result.Error)
		}
		if result.RowsAffected > 0 {
			updated++
		} else {
			skipped++
		}
	}

	// 记录当前驱动供输出参考
	_ = config.Cfg.DatabaseDriver
	return updated, skipped, nil
}
