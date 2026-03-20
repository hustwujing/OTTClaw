// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/middleware/session.go — 校验 session_id 存在且归属当前 user_id
// 必须在 JWTAuth 之后使用（依赖 user_id 已注入 context）
package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

const (
	// SessionIDKey Gin context 中存储 session_id 的 key
	SessionIDKey = "session_id"
)

// SessionOwner 返回会话归属校验中间件
// 从 ?session_id 查询参数读取 session_id，校验其归属当前已鉴权的 user_id
func SessionOwner() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		userID := GetUserID(c)
		sessionID := c.Query("session_id")

		if sessionID == "" {
			logger.Warn("session", userID, "", "missing session_id", time.Since(start))
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "session_id is required",
			})
			return
		}

		sess, err := storage.GetSession(sessionID)
		if err != nil {
			logger.Error("session", userID, sessionID, "db query session failed", err, time.Since(start))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "internal error",
			})
			return
		}
		if sess == nil {
			logger.Warn("session", userID, sessionID, "session not found", time.Since(start))
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "session not found",
			})
			return
		}

		// 核心安全校验：session 必须属于当前 user_id
		if sess.UserID != userID {
			logger.Warn("session", userID, sessionID, "session ownership check failed", time.Since(start))
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "session does not belong to current user",
			})
			return
		}

		logger.Info("session", userID, sessionID, "session ownership verified", time.Since(start))
		c.Set(SessionIDKey, sessionID)
		c.Next()
	}
}

// GetSessionID 从 Gin context 中安全地读取 session_id
func GetSessionID(c *gin.Context) string {
	v, _ := c.Get(SessionIDKey)
	id, _ := v.(string)
	return id
}
