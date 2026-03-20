// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/handler/download.go — GET /download/:token
// 根据临时 token 向用户提供文件下载，token 由 serve_file_download 工具生成。
package handler

import (
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/tool"
)

// Download 凭 token 提供文件下载，token 过期或不存在时返回 404。
func Download(c *gin.Context) {
	token := c.Param("token")
	filePath, filename, ok := tool.LookupDLToken(token)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "download link expired or not found"})
		return
	}
	c.Header(
		"Content-Disposition",
		`attachment; filename*=UTF-8''`+url.PathEscape(filename),
	)
	c.File(filePath)
}
