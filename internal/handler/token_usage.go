// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/handler/token_usage.go — GET /api/token-usage
// 返回当前用户的历史 token 消耗（分输入/输出）
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/middleware"
	"OTTClaw/internal/storage"
)

// GetTokenUsage 处理 GET /api/token-usage
func GetTokenUsage(c *gin.Context) {
	userID := middleware.GetUserID(c)
	summary, err := storage.GetUserTokenSummary(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"prompt_tokens":     summary.PromptTokens,
		"completion_tokens": summary.CompletionTokens,
	})
}
