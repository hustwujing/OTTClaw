// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/handler/app_info.go — GET /api/app-info
// 返回当前 ROLE.md 中解析出的应用显示名称和头像 URL，供前端动态更新标题栏和对话气泡。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/agent"
	"OTTClaw/internal/storage"
)

// GetAppInfo 返回应用名称（取自 ROLE.md 第一个一级标题）和头像 URL。
func GetAppInfo(c *gin.Context) {
	name := agent.GetRoleName()
	if name == "" {
		name = "多屏工具箱"
	}
	avatarURL := ""
	if cfg, err := storage.GetAppConfig(); err == nil {
		avatarURL = cfg.AvatarURL
	}
	c.JSON(http.StatusOK, gin.H{"name": name, "avatar_url": avatarURL})
}
