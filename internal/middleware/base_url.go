// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/middleware/base_url.go — 从每次 HTTP 请求中提取 scheme://host，
// 缓存为服务对外访问的 base URL，供工具侧（如 output_file）拼接完整链接使用。
package middleware

import (
	"github.com/gin-gonic/gin"

	"OTTClaw/config"
	"OTTClaw/internal/storage"
	"OTTClaw/internal/tool"
)

// BaseURL 是全局中间件，从 HTTP 请求中提取 scheme://host 并写入缓存。
// 支持 X-Forwarded-Proto 和 X-Forwarded-Host（适配反向代理 / HTTPS 终止）。
// 若 SERVER_PUBLIC_URL 已显式配置，则跳过动态推断（不覆盖固定公网地址）。
func BaseURL() gin.HandlerFunc {
	return func(c *gin.Context) {
		// SERVER_PUBLIC_URL 已配置时，不从请求头动态推断（避免覆盖固定地址）
		if config.Cfg.ServerPublicURL != "" {
			c.Next()
			return
		}
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
