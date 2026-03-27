// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/storage/db.go — 数据库连接（SQLite / MySQL）、AutoMigrate、CRUD 封装
package storage

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/glebarez/sqlite" // 纯 Go SQLite 驱动，无需 CGO
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"OTTClaw/config"
)

// DB 全局数据库连接，服务启动后设置
var DB *gorm.DB

// InitDB 根据 DATABASE_DRIVER 配置初始化数据库，执行自动迁移建表。
// "sqlite"（默认）：使用 DATABASE_PATH 作为文件路径；
// "mysql"：使用 DATABASE_HOST / PORT / NAME / USER / PASSWORD 构建 DSN。
func InitDB() error {
	cfg := config.Cfg

	gormCfg := &gorm.Config{
		Logger:                                   gormlogger.Default.LogMode(gormlogger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
	}

	var db *gorm.DB
	var err error

	switch cfg.DatabaseDriver {
	case "mysql":
		dsn := buildMySQLDSN(cfg)
		db, err = gorm.Open(mysql.Open(dsn), gormCfg)
		if err != nil {
			return fmt.Errorf("open mysql: %w", err)
		}
	default: // "sqlite" 或空
		db, err = gorm.Open(sqlite.Open(cfg.DatabasePath), gormCfg)
		if err != nil {
			return fmt.Errorf("open sqlite: %w", err)
		}
		// SQLite 文件级锁：限制为单连接，由 Go 连接池串行化所有操作，
		// 防止多 goroutine 并发写时出现 "database is locked" 错误。
		if sqlDB, e := db.DB(); e == nil {
			sqlDB.SetMaxOpenConns(1)
			sqlDB.SetMaxIdleConns(1)
		}
	}

	// 自动建表 / 更新表结构
	if err := db.AutoMigrate(&InviteCode{}, &Session{}, &SessionMessage{}, &ToolRequest{}, &FeishuConfig{}, &WeComConfig{}, &CronJob{}, &UserProfile{}, &UserData{}, &TokenUsage{}, &OriginSessionMessage{}); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}

	DB = db

	// SQLite FTS5 全文索引：为 origin_session_messages 建外部内容虚拟表 + 同步触发器
	// MySQL 不支持 FTS5，跳过
	if cfg.DatabaseDriver != "mysql" {
		_ = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			content,
			content="origin_session_messages",
			content_rowid="id",
			tokenize="unicode61"
		)`).Error
		_ = db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_insert
			AFTER INSERT ON origin_session_messages BEGIN
				INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
			END`).Error
		_ = db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_delete
			AFTER DELETE ON origin_session_messages BEGIN
				INSERT INTO messages_fts(messages_fts, rowid, content)
				VALUES ('delete', old.id, old.content);
			END`).Error
	}

	return nil
}

// buildMySQLDSN 根据配置构建 MySQL DSN。
// 格式：user:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
func buildMySQLDSN(cfg *config.AppConfig) string {
	// URL 编码密码，防止特殊字符导致 DSN 解析失败
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=%s",
		cfg.DatabaseUser,
		url.QueryEscape(cfg.DatabasePassword),
		cfg.DatabaseHost,
		cfg.DatabasePort,
		cfg.DatabaseName,
		url.QueryEscape("Local"),
	)
}

// ----- InviteCode CRUD -----

// 邀请码专用错误，供上层按类型判断
var (
	ErrInviteNotFound = fmt.Errorf("invite code not found")
	ErrInviteExpired  = fmt.Errorf("invite code expired")
	ErrInviteMaxUses  = fmt.Errorf("invite code device limit reached")
)

// CreateInviteCode 写入一条邀请码记录
// maxUses: 最多允许登录的设备数，0 表示不限
func CreateInviteCode(code, userID string, maxUses int, expiresAt *time.Time) error {
	return DB.Create(&InviteCode{
		Code:      code,
		UserID:    userID,
		MaxUses:   maxUses,
		ExpiresAt: expiresAt,
	}).Error
}

// UseInviteCode 在事务中校验邀请码并原子递增 use_count。
// 返回记录本身（含 UserID）供上层签发 Token。
// 失败时返回 ErrInviteNotFound / ErrInviteExpired / ErrInviteMaxUses。
func UseInviteCode(code string) (*InviteCode, error) {
	var record InviteCode
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("code = ?", code).First(&record).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return ErrInviteNotFound
			}
			return err
		}
		if record.ExpiresAt != nil && time.Now().After(*record.ExpiresAt) {
			return ErrInviteExpired
		}
		if record.MaxUses > 0 && record.UseCount >= record.MaxUses {
			return ErrInviteMaxUses
		}
		return tx.Model(&record).
			UpdateColumn("use_count", gorm.Expr("use_count + 1")).Error
	})
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// DeleteInviteCode 删除指定邀请码
func DeleteInviteCode(code string) error {
	return DB.Delete(&InviteCode{}, "code = ?", code).Error
}

// ----- Session CRUD -----

// CreateSession 创建新会话记录（source 默认 "web"）
func CreateSession(sessionID, userID string) error {
	return CreateSessionWithSource(sessionID, userID, "web")
}

// CreateSessionWithSource 创建新会话记录，可指定来源（如 "web"、"feishu"、"cron"）
func CreateSessionWithSource(sessionID, userID, source string) error {
	s := &Session{
		SessionID: sessionID,
		UserID:    userID,
		KVData:    "{}",
		Source:    source,
	}
	return DB.Create(s).Error
}

// GetSession 按 session_id 查询会话，不存在返回 (nil, nil)
func GetSession(sessionID string) (*Session, error) {
	var s Session
	result := DB.Where("session_id = ?", sessionID).First(&s)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &s, result.Error
}

// UpdateSessionKV 将 map 序列化为 JSON 后更新 kv_data 字段
func UpdateSessionKV(sessionID string, kv map[string]interface{}) error {
	data, err := json.Marshal(kv)
	if err != nil {
		return fmt.Errorf("marshal kv: %w", err)
	}
	return DB.Model(&Session{}).
		Where("session_id = ?", sessionID).
		Update("kv_data", string(data)).Error
}

// GetSessionKV 读取 kv_data 并反序列化为 map
func GetSessionKV(sessionID string) (map[string]interface{}, error) {
	s, err := GetSession(sessionID)
	if err != nil || s == nil {
		return map[string]interface{}{}, err
	}
	var kv map[string]interface{}
	if err := json.Unmarshal([]byte(s.KVData), &kv); err != nil {
		return map[string]interface{}{}, nil
	}
	return kv, nil
}

// ----- SessionMessage CRUD -----

// AddMessage 向会话添加一条消息。
// toolCallsJSON：assistant 角色且含工具调用时传入 JSON 字符串（用于跨轮次重建 ToolCalls），其他情况传 ""。
// 写入消息后同步 touch sessions.updated_at，使侧栏能按最近活跃时间排序。
func AddMessage(userID, sessionID, role, content, toolCallID, name, toolCallsJSON string) error {
	msg := &SessionMessage{
		UserID:        userID,
		SessionID:     sessionID,
		Role:          role,
		Content:       content,
		ToolCallID:    toolCallID,
		Name:          name,
		ToolCallsJSON: toolCallsJSON,
	}
	if err := DB.Create(msg).Error; err != nil {
		return err
	}
	// touch session updated_at，供侧栏按活跃时间排序（忽略错误，非关键操作）
	DB.Exec("UPDATE sessions SET updated_at = ? WHERE session_id = ?", time.Now(), sessionID)
	return nil
}

// GetMessages 按 session_id 查询全部消息，按时间升序
func GetMessages(sessionID string) ([]SessionMessage, error) {
	var msgs []SessionMessage
	err := DB.Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Find(&msgs).Error
	return msgs, err
}

// CountUserMessages 统计指定会话中 user 角色的消息数
func CountUserMessages(sessionID string) (int64, error) {
	var count int64
	err := DB.Model(&SessionMessage{}).
		Where("session_id = ? AND role = ?", sessionID, "user").
		Count(&count).Error
	return count, err
}

// DeleteSession 删除会话及其所有消息（事务保证原子性，userID 用于鉴权）
func DeleteSession(sessionID, userID string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id = ? AND user_id = ?", sessionID, userID).
			Delete(&SessionMessage{}).Error; err != nil {
			return fmt.Errorf("delete messages: %w", err)
		}
		res := tx.Where("session_id = ? AND user_id = ?", sessionID, userID).
			Delete(&Session{})
		if res.Error != nil {
			return fmt.Errorf("delete session: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("session not found or access denied")
		}
		return nil
	})
}

// SetSessionParent 设置会话的父会话 ID，用于显式续话或压缩衍生时建立血缘链。
func SetSessionParent(sessionID, parentSessionID string) error {
	return DB.Model(&Session{}).
		Where("session_id = ?", sessionID).
		Update("parent_session_id", parentSessionID).Error
}

// GetSessionLineage 沿 parent_session_id 链向上溯源，返回包含 sessionID 本身在内的
// 完整血缘链（从自身到根，最多追溯 20 层，防止环路死循环）。
// 结果用于 session_search 排除同一会话血缘的所有历史碎片。
func GetSessionLineage(sessionID string) ([]string, error) {
	const maxDepth = 20
	lineage := make([]string, 0, 4)
	cur := sessionID
	for i := 0; i < maxDepth && cur != ""; i++ {
		lineage = append(lineage, cur)
		var s Session
		result := DB.Select("parent_session_id").Where("session_id = ?", cur).First(&s)
		if result.Error != nil {
			break // 找不到则终止，不报错
		}
		cur = s.ParentSessionID
	}
	return lineage, nil
}

// UpdateSessionTitle 更新会话的 AI 生成标题
func UpdateSessionTitle(sessionID, title string) error {
	return DB.Model(&Session{}).
		Where("session_id = ?", sessionID).
		Update("title", title).Error
}

// GetUserSessions 按 updated_at 倒序返回用户所有来源的会话（内部使用）
func GetUserSessions(userID string) ([]Session, error) {
	var sessions []Session
	err := DB.Where("user_id = ?", userID).
		Order("updated_at DESC").
		Find(&sessions).Error
	return sessions, err
}

// GetUserWebSessions 按 updated_at 倒序返回用户的 Web 来源会话（前端侧栏使用）
func GetUserWebSessions(userID string) ([]Session, error) {
	var sessions []Session
	err := DB.Where("user_id = ? AND (source = 'web' OR source = '')", userID).
		Order("updated_at DESC").
		Find(&sessions).Error
	return sessions, err
}

// SessionPreview 会话摘要信息（含 AI 标题或第一条用户消息预览）
type SessionPreview struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Title     string    `json:"title"`   // AI 生成的标题（可为空）
	Preview   string    `json:"preview"` // 第一条 user 消息（前端 fallback）
}

// GetUserSessionPreviews 返回用户所有 Web 会话摘要，按 updated_at 倒序。
// 过滤掉飞书来源的会话，避免污染 Web 侧栏。
// 每条记录附带第一条 user 消息的前 50 个字符作为预览。
func GetUserSessionPreviews(userID string) ([]SessionPreview, error) {
	sessions, err := GetUserWebSessions(userID)
	if err != nil {
		return nil, err
	}
	result := make([]SessionPreview, 0, len(sessions))
	for _, s := range sessions {
		preview := ""
		var firstMsg SessionMessage
		if err2 := DB.Where("session_id = ? AND role = ?", s.SessionID, "user").
			Order("id ASC").First(&firstMsg).Error; err2 == nil {
			runes := []rune(firstMsg.Content)
			if len(runes) > 50 {
				preview = string(runes[:50]) + "…"
			} else {
				preview = firstMsg.Content
			}
		}
		result = append(result, SessionPreview{
			SessionID: s.SessionID,
			CreatedAt: s.CreatedAt,
			UpdatedAt: s.UpdatedAt,
			Title:     s.Title,
			Preview:   preview,
		})
	}
	return result, nil
}

// GetDisplayMessages 返回会话中可展示的消息（user + assistant 角色），按创建时间升序。
// 过滤掉 tool / system 等内部角色，供前端历史回放使用。
func GetDisplayMessages(sessionID string) ([]SessionMessage, error) {
	var msgs []SessionMessage
	err := DB.Where("session_id = ? AND role IN ('user','assistant')", sessionID).
		Order("created_at ASC").
		Find(&msgs).Error
	return msgs, err
}

// CompressMessages 用摘要替换旧消息，保留最新的 keepRecent 条不动。
//
// 事务流程：
//  1. 删除该会话全部消息
//  2. 插入一条 role=system 的摘要消息（标记为历史压缩）
//  3. 重新插入最近 keepRecent 条消息
//
// 若消息总数 ≤ keepRecent，直接返回（无需压缩）。
func CompressMessages(sessionID, userID, summary string, keepRecent int, recentMsgs []SessionMessage) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		// 删除该会话所有消息
		if err := tx.Where("session_id = ?", sessionID).Delete(&SessionMessage{}).Error; err != nil {
			return fmt.Errorf("delete old messages: %w", err)
		}

		// 插入摘要消息（role=system，方便 buildMessages 识别并放在靠前位置）
		summaryMsg := &SessionMessage{
			UserID:    userID,
			SessionID: sessionID,
			Role:      "system",
			Content:   "[历史对话摘要]\n" + summary,
		}
		if err := tx.Create(summaryMsg).Error; err != nil {
			return fmt.Errorf("insert summary message: %w", err)
		}

		// 重新插入保留的最近消息
		for i := range recentMsgs {
			recentMsgs[i].ID = 0 // 重置主键，让 DB 自动生成新 ID
			if err := tx.Create(&recentMsgs[i]).Error; err != nil {
				return fmt.Errorf("re-insert message: %w", err)
			}
		}
		return nil
	})
}

// ----- ToolRequest CRUD -----

// CreateToolRequest 写入一条工具需求记录
func CreateToolRequest(r *ToolRequest) error {
	return DB.Create(r).Error
}

// ListToolRequests 按创建时间倒序返回工具需求记录
// status 为空时返回全部，否则按 status 过滤（pending / done）
func ListToolRequests(status string) ([]ToolRequest, error) {
	var rows []ToolRequest
	q := DB.Order("created_at DESC")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	err := q.Find(&rows).Error
	return rows, err
}

// UpdateToolRequestStatus 更新单条工具需求的状态
func UpdateToolRequestStatus(id uint, status string) error {
	return DB.Model(&ToolRequest{}).Where("id = ?", id).Update("status", status).Error
}

// CloseToolRequest 将指定工具需求标记为 done，并记录关闭原因
func CloseToolRequest(id uint, reason string) error {
	return DB.Model(&ToolRequest{}).Where("id = ?", id).
		Updates(map[string]any{"status": "done", "close_reason": reason}).Error
}

// MarkImplementedToolRequests 将 names 列表中已实现的工具需求标记为 done
// 仅更新当前为 pending 的记录，避免误覆盖手动调整过的状态
func MarkImplementedToolRequests(names []string) error {
	if len(names) == 0 {
		return nil
	}
	return DB.Model(&ToolRequest{}).
		Where("status = ? AND name IN ?", "pending", names).
		Update("status", "done").Error
}

// ----- UserProfile CRUD -----

// GetUserProfile 按 user_id 查询用户人设，不存在返回 (nil, nil)
func GetUserProfile(userID string) (*UserProfile, error) {
	var p UserProfile
	result := DB.Where("user_id = ?", userID).First(&p)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &p, result.Error
}

// UpsertUserProfile 创建或更新用户人设（仅更新 persona 列，不覆盖 notes）
func UpsertUserProfile(userID, persona string) error {
	result := DB.Model(&UserProfile{}).Where("user_id = ?", userID).Update("persona", persona)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return DB.Create(&UserProfile{UserID: userID, Persona: persona}).Error
	}
	return nil
}

// GetUserNotes 读取用户笔记，用户不存在时返回 ("", nil)
func GetUserNotes(userID string) (string, error) {
	var p UserProfile
	result := DB.Select("notes").Where("user_id = ?", userID).First(&p)
	if result.Error == gorm.ErrRecordNotFound {
		return "", nil
	}
	return p.Notes, result.Error
}

// UpsertUserNotes 覆写用户笔记（仅更新 notes 列，不覆盖 persona）
func UpsertUserNotes(userID, notes string) error {
	result := DB.Model(&UserProfile{}).Where("user_id = ?", userID).Update("notes", notes)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return DB.Create(&UserProfile{UserID: userID, Notes: notes}).Error
	}
	return nil
}

// ----- UserData CRUD -----

// GetUserData 按 user_id + key 查询单条值，不存在时返回 ("", false, nil)
func GetUserData(userID, key string) (string, bool, error) {
	var row UserData
	result := DB.Where("user_id = ? AND key = ?", userID, key).First(&row)
	if result.Error == gorm.ErrRecordNotFound {
		return "", false, nil
	}
	if result.Error != nil {
		return "", false, result.Error
	}
	return row.Value, true, nil
}

// SetUserData 创建或覆写 user_id + key 对应的值（upsert）
func SetUserData(userID, key, value string) error {
	result := DB.Model(&UserData{}).
		Where("user_id = ? AND key = ?", userID, key).
		Update("value", value)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return DB.Create(&UserData{UserID: userID, Key: key, Value: value}).Error
	}
	return nil
}

// DeleteUserData 删除 user_id + key 对应的行，key 不存在时返回 (false, nil)
func DeleteUserData(userID, key string) (bool, error) {
	result := DB.Where("user_id = ? AND key = ?", userID, key).Delete(&UserData{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// ListUserData 返回用户所有 KV 条目（key → value 映射）
func ListUserData(userID string) (map[string]string, error) {
	var rows []UserData
	if err := DB.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	kv := make(map[string]string, len(rows))
	for _, r := range rows {
		kv[r.Key] = r.Value
	}
	return kv, nil
}

// CountUserData 统计用户当前条目数
func CountUserData(userID string) (int64, error) {
	var count int64
	err := DB.Model(&UserData{}).Where("user_id = ?", userID).Count(&count).Error
	return count, err
}

// ----- TokenUsage CRUD -----

// UserTokenSummary 用户 token 消耗汇总
type UserTokenSummary struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

// GetUserTokenSummary 统计指定用户的历史 token 消耗（分输入/输出）
func GetUserTokenSummary(userID string) (UserTokenSummary, error) {
	var s UserTokenSummary
	err := DB.Model(&TokenUsage{}).
		Where("user_id = ?", userID).
		Select("COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens").
		Scan(&s).Error
	return s, err
}

// AddTokenUsage 写入一条 LLM token 消耗记录
func AddTokenUsage(userID, sessionID, model string, prompt, completion int) error {
	return DB.Create(&TokenUsage{
		UserID:           userID,
		SessionID:        sessionID,
		Model:            model,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}).Error
}

// ----- OriginSessionMessage CRUD -----

// AddOriginMessage 写入一条用户可见历史消息（不截断，支持附件）。
// attachments 为 nil 或空切片时 Attachments 字段存空字符串。
func AddOriginMessage(userID, sessionID, role, content string, attachments []Attachment) error {
	attJSON := ""
	if len(attachments) > 0 {
		b, _ := json.Marshal(attachments)
		attJSON = string(b)
	}
	return DB.Create(&OriginSessionMessage{
		UserID:      userID,
		SessionID:   sessionID,
		Role:        role,
		Content:     content,
		Attachments: attJSON,
	}).Error
}

// GetOriginMessages 按 session_id 查询全部用户可见消息，按时间升序
func GetOriginMessages(sessionID string) ([]OriginSessionMessage, error) {
	var msgs []OriginSessionMessage
	err := DB.Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Find(&msgs).Error
	return msgs, err
}

// ----- 跨会话全文搜索 -----

// SessionSearchHit 单条命中消息（FTS5 / LIKE 搜索结果）
type SessionSearchHit struct {
	SessionID string
	Content   string
	CreatedAt time.Time
}

// SessionHitGroup 一个 session 中所有命中消息的集合
type SessionHitGroup struct {
	SessionID string
	Hits      []SessionSearchHit
	LastHitAt time.Time // 最新命中消息时间，用于排序
}

// GetRecentSessionMeta 按 updated_at 倒序返回用户最近 limit 条会话的元数据，
// 不含 FTS 搜索，不调用 LLM，供 session_search 无查询词模式使用。
// 排除 excludeSessionID（当前会话），limit 默认 5，最多 10。
func GetRecentSessionMeta(userID, excludeSessionID string, limit int) ([]SessionPreview, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	var sessions []Session
	err := DB.Where("user_id = ? AND session_id != ?", userID, excludeSessionID).
		Order("updated_at DESC").
		Limit(limit).
		Find(&sessions).Error
	if err != nil {
		return nil, err
	}
	result := make([]SessionPreview, 0, len(sessions))
	for _, s := range sessions {
		preview := ""
		var firstMsg SessionMessage
		if err2 := DB.Where("session_id = ? AND role = ?", s.SessionID, "user").
			Order("id ASC").First(&firstMsg).Error; err2 == nil {
			runes := []rune(firstMsg.Content)
			if len(runes) > 50 {
				preview = string(runes[:50]) + "…"
			} else {
				preview = firstMsg.Content
			}
		}
		result = append(result, SessionPreview{
			SessionID: s.SessionID,
			CreatedAt: s.CreatedAt,
			UpdatedAt: s.UpdatedAt,
			Title:     s.Title,
			Preview:   preview,
		})
	}
	return result, nil
}

// SearchSessionMessages 在当前用户历史会话中全文搜索，排除 excludeSessionIDs 列表中的所有会话
// （通常为当前会话的完整血缘链，由 GetSessionLineage 获取），
// 返回 top maxSessions 个 session 各自的命中消息（按 FTS5 rank 或关键词频次排序）。
// SQLite 使用 FTS5；MySQL 降级为 LIKE 搜索。
// 每个 SessionHitGroup 最多包含 5 条命中消息，结果按最近命中时间倒序。
func SearchSessionMessages(userID, query string, excludeSessionIDs []string, maxSessions int) ([]SessionHitGroup, error) {
	if maxSessions <= 0 {
		maxSessions = 5
	}

	// 构建排除集合，加速过滤
	excludeSet := make(map[string]bool, len(excludeSessionIDs))
	for _, sid := range excludeSessionIDs {
		excludeSet[sid] = true
	}
	// 取第一个 ID 用于 SQL 单值占位符（兼容旧逻辑），多余的在 Go 层过滤
	excludePrimary := ""
	if len(excludeSessionIDs) > 0 {
		excludePrimary = excludeSessionIDs[0]
	}

	type row struct {
		SessionID string
		Content   string
		CreatedAt time.Time
	}

	var rows []row
	var err error

	if config.Cfg.DatabaseDriver == "mysql" {
		// MySQL 降级：LIKE 搜索（不支持 FTS5）
		likeQuery := "%" + query + "%"
		err = DB.Raw(`
			SELECT session_id, content, created_at
			FROM origin_session_messages
			WHERE user_id = ? AND session_id != ? AND content LIKE ?
			ORDER BY created_at DESC
			LIMIT 100
		`, userID, excludePrimary, likeQuery).Scan(&rows).Error
	} else {
		// SQLite FTS5 搜索
		err = DB.Raw(`
			SELECT m.session_id, m.content, m.created_at
			FROM origin_session_messages m
			JOIN messages_fts fts ON fts.rowid = m.id
			WHERE fts.content MATCH ?
			  AND m.user_id = ?
			  AND m.session_id != ?
			ORDER BY fts.rank
			LIMIT 100
		`, query, userID, excludePrimary).Scan(&rows).Error
	}
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}

	// 按 session_id 分组（Go 层二次过滤，覆盖血缘链中非 excludePrimary 的其他会话）
	groupMap := make(map[string]*SessionHitGroup)
	var groupOrder []string // 保持首次出现顺序（FTS5 按 rank，LIKE 按时间）
	for _, r := range rows {
		if excludeSet[r.SessionID] { // 血缘链中除 excludePrimary 之外的其他祖先会话
			continue
		}
		g, ok := groupMap[r.SessionID]
		if !ok {
			if len(groupOrder) >= maxSessions {
				continue
			}
			g = &SessionHitGroup{SessionID: r.SessionID}
			groupMap[r.SessionID] = g
			groupOrder = append(groupOrder, r.SessionID)
		}
		if len(g.Hits) < 5 {
			g.Hits = append(g.Hits, SessionSearchHit{
				SessionID: r.SessionID,
				Content:   r.Content,
				CreatedAt: r.CreatedAt,
			})
			if r.CreatedAt.After(g.LastHitAt) {
				g.LastHitAt = r.CreatedAt
			}
		}
	}

	result := make([]SessionHitGroup, 0, len(groupOrder))
	for _, sid := range groupOrder {
		result = append(result, *groupMap[sid])
	}
	return result, nil
}
