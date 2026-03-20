// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/handler/session.go — POST /api/session/create
// 鉴权通过后为当前用户创建新会话，返回服务端生成的 session_id
package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"OTTClaw/internal/logger"
	"OTTClaw/internal/middleware"
	"OTTClaw/internal/storage"
)

// CreateSessionRequest 请求体（当前无需额外参数，预留扩展）
type CreateSessionRequest struct {
	// 可扩展：如初始 KV 数据、会话标签等
}

// CreateSessionResponse 响应体
type CreateSessionResponse struct {
	SessionID string `json:"session_id"`
}

// CreateSession 处理 POST /api/session/create
func CreateSession(c *gin.Context) {
	start := time.Now()
	userID := middleware.GetUserID(c)

	// 生成服务端 UUID 作为 session_id，客户端不可指定
	sessionID := uuid.New().String()

	if err := storage.CreateSession(sessionID, userID); err != nil {
		logger.Error("session", userID, sessionID, "create session failed", err, time.Since(start))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create session failed"})
		return
	}

	logger.Info("session", userID, sessionID, "session created", time.Since(start))
	c.JSON(http.StatusOK, CreateSessionResponse{SessionID: sessionID})
}

// ListSessions 处理 GET /api/sessions
// 返回当前用户的所有会话（含第一条用户消息预览），按最近活跃时间倒序
func ListSessions(c *gin.Context) {
	start := time.Now()
	userID := middleware.GetUserID(c)

	previews, err := storage.GetUserSessionPreviews(userID)
	if err != nil {
		logger.Error("session", userID, "", "list sessions failed", err, time.Since(start))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load sessions failed"})
		return
	}
	if previews == nil {
		previews = []storage.SessionPreview{}
	}
	c.JSON(http.StatusOK, gin.H{"sessions": previews})
}

// DeleteSession 处理 DELETE /api/session/:session_id
func DeleteSession(c *gin.Context) {
	start := time.Now()
	userID := middleware.GetUserID(c)
	sessionID := c.Param("session_id")

	if err := storage.DeleteSession(sessionID, userID); err != nil {
		logger.Error("session", userID, sessionID, "delete session failed", err, time.Since(start))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	logger.Info("session", userID, sessionID, "session deleted", time.Since(start))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GetSessionMessages 处理 GET /api/session/messages?session_id=xxx
// 从 origin_session_messages 读取用户可见历史（含附件），供前端历史回放使用
func GetSessionMessages(c *gin.Context) {
	start := time.Now()
	sessionID := c.Query("session_id")
	userID := middleware.GetUserID(c)

	msgs, err := storage.GetOriginMessages(sessionID)
	if err != nil {
		logger.Error("session", userID, sessionID, "get messages failed", err, time.Since(start))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load messages failed"})
		return
	}

	type MsgDTO struct {
		Role        string               `json:"role"`
		Content     string               `json:"content"`
		Attachments []storage.Attachment `json:"attachments,omitempty"`
		CreatedAt   time.Time            `json:"created_at"`
	}
	dtos := make([]MsgDTO, 0, len(msgs))
	for _, m := range msgs {
		// 同时为空才跳过；纯附件记录（content=""，attachments 非空）仍需保留
		if m.Content == "" && m.Attachments == "" {
			continue
		}
		var atts []storage.Attachment
		if m.Attachments != "" {
			_ = json.Unmarshal([]byte(m.Attachments), &atts)
		}
		dtos = append(dtos, MsgDTO{
			Role:        m.Role,
			Content:     m.Content,
			Attachments: atts,
			CreatedAt:   m.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"messages": dtos})
}
