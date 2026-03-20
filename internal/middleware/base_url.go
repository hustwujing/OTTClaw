// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/middleware/base_url.go — 从每次 HTTP 请求中提取 scheme://host，
// 缓存为服务对外访问的 base URL，供工具侧（如 output_file）拼接完整链接使用。
package middleware

import (
	"github.com/gin-gonic/gin"

	"OTTClaw/internal/storage"
	"OTTClaw/internal/tool"
)

// BaseURL 是全局中间件，从 HTTP 请求中提取 scheme://host 并写入缓存。
// 支持 X-Forwarded-Proto 和 X-Forwarded-Host（适配反向代理 / HTTPS 终止）。
func BaseURL() gin.HandlerFunc {
	return func(c *gin.Context) {
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		} else if proto := c.GetHeader("X-Forwarded-Proto"); proto == "https" {
			scheme = "https"
		}
		host := c.Request.Host
		if fwdHost := c.GetHeader("X-Forwarded-Host"); fwdHost != "" {
			host = fwdHost
		}
		if host != "" {
			base := scheme + "://" + host
			tool.SetServerBaseURL(base)
			// 异步持久化，避免阻塞请求；写文件内有去重，实际 IO 极少
			go func() { _ = storage.SetServiceBaseURL(base) }()
		}
		c.Next()
	}
}
