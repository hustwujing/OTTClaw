// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/handler/auth.go — POST /api/auth/login（公开接口，无需 JWT）
// 用邀请码换取 JWT Token
package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/auth"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

type loginRequest struct {
	InviteCode string `json:"invite_code" binding:"required"`
}

type loginResponse struct {
	Token  string `json:"token"`
	UserID string `json:"user_id"`
}

// Login 处理 POST /api/auth/login
func Login(c *gin.Context) {
	start := time.Now()

	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invite_code is required"})
		return
	}

	record, err := storage.UseInviteCode(req.InviteCode)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrInviteNotFound):
			logger.Warn("auth", "", "", "invite code not found", time.Since(start))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "邀请码无效"})
		case errors.Is(err, storage.ErrInviteExpired):
			logger.Warn("auth", "", "", "invite code expired", time.Since(start))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "邀请码已过期"})
		case errors.Is(err, storage.ErrInviteMaxUses):
			logger.Warn("auth", "", "", "invite code device limit reached", time.Since(start))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "邀请码设备数已达上限"})
		default:
			logger.Error("auth", "", "", "use invite code", err, time.Since(start))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		}
		return
	}

	token, err := auth.GenerateToken(record.UserID, 30*24*time.Hour)
	if err != nil {
		logger.Error("auth", record.UserID, "", "generate token", err, time.Since(start))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "generate token failed"})
		return
	}

	logger.Info("auth", record.UserID, "", "login ok", time.Since(start))
	c.JSON(http.StatusOK, loginResponse{Token: token, UserID: record.UserID})
}
