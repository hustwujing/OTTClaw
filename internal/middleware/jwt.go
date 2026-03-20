// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/middleware/jwt.go — Gin JWT 鉴权中间件
// 从 Authorization header 或 token 查询参数提取并验证 JWT
// 验证通过后将 user_id 注入到 Gin context
package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/auth"
	"OTTClaw/internal/logger"
)

const (
	// UserIDKey Gin context 中存储 user_id 的 key
	UserIDKey = "user_id"
)

// JWTAuth 返回 JWT 鉴权中间件
// 支持从 Authorization: Bearer <token> 或 ?token=<token> 中提取
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		tokenStr := extractToken(c)

		if tokenStr == "" {
			logger.Warn("auth", "", "", "missing token", time.Since(start))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing authorization token",
			})
			return
		}

		userID, err := auth.ParseToken(tokenStr)
		if err != nil {
			logger.Error("auth", "", "", "jwt parse failed", err, time.Since(start))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid or expired token",
			})
			return
		}

		logger.Info("auth", userID, "", "jwt verified ok", time.Since(start))
		// 将解析出的 user_id 注入到请求上下文
		c.Set(UserIDKey, userID)
		c.Next()
	}
}

// GetUserID 从 Gin context 中安全地读取 user_id
func GetUserID(c *gin.Context) string {
	v, _ := c.Get(UserIDKey)
	id, _ := v.(string)
	return id
}

// extractToken 优先从 Authorization header，其次从 ?token 查询参数中提取 JWT 字符串
func extractToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if header != "" {
		token, err := auth.ExtractTokenFromBearer(header)
		if err == nil {
			return token
		}
	}
	// WebSocket/SSE 场景：token 通过 query param 传递
	return c.Query("token")
}
