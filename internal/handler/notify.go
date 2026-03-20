// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/handler/notify.go — GET /api/notify — 服务端主动推送（SSE）
// 前端打开某个 session 时订阅此端点，cron 任务运行时会实时推送事件
package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/middleware"
	"OTTClaw/internal/push"
)

// Notify 处理 GET /api/notify?session_id=xxx&token=xxx
// 路由层已挂载 JWTAuth + SessionOwner 中间件
func Notify(c *gin.Context) {
	sessionID := middleware.GetSessionID(c)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	ch, cancel := push.Default.Subscribe(sessionID)
	defer cancel()

	// 每 25 秒发送一次心跳，防止代理/浏览器因超时断开连接
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			flusher.Flush()
		case <-c.Request.Context().Done():
			return
		}
	}
}
