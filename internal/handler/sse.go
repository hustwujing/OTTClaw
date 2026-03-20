// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/handler/sse.go — POST /sse?session_id=xxx&token=xxx
// 客户端以 POST 发送 message，服务端以 text/event-stream 流式返回回答
// 每次请求对应一轮对话（一问一答），长连接通过 WebSocket 更合适
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/agent"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/middleware"
)

// sseRequest 请求体
type sseRequest struct {
	Message string `json:"message" binding:"required"`
}

// SSE 处理 POST /sse
// 中间件 JWTAuth + SessionOwner 已在路由层挂载
func SSE(c *gin.Context) {
	start := time.Now()
	userID := middleware.GetUserID(c)
	sessionID := middleware.GetSessionID(c)

	// 解析请求体
	var req sseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	logger.Info("sse", userID, sessionID, "sse request received", time.Since(start))

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 关闭 Nginx 缓冲

	// 获取底层 http.ResponseWriter 并断言支持 Flush
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		logger.Error("sse", userID, sessionID, "streaming unsupported", nil, 0)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	writer := &sseWriter{
		w:       c.Writer,
		flusher: flusher,
	}

	// 用独立 context 驱动 Agent，与请求 context 解耦：
	// 客户端断连时主动 cancel，确保 Agent goroutine 快速退出后再返回，
	// 避免 goroutine 持有已回收的 ResponseWriter 引发 nil pointer panic。
	agentCtx, agentCancel := context.WithCancel(context.Background())
	defer agentCancel()

	done := make(chan error, 1)
	go func() {
		done <- agent.Get().Run(agentCtx, userID, sessionID, req.Message, writer)
	}()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			if err != nil {
				logger.Error("sse", userID, sessionID, "agent run failed", err, time.Since(start))
			}
			logger.Info("sse", userID, sessionID, "sse request done", time.Since(start))
			return
		case <-ticker.C:
			writer.writeHeartbeat()
		case <-c.Request.Context().Done():
			logger.Info("sse", userID, sessionID, "sse client disconnected", time.Since(start))
			agentCancel() // 取消 agent context，使进行中的 browser HTTP 请求立即中止
			select {
			case <-done: // 等待 goroutine 退出后再释放 ResponseWriter
			case <-time.After(10 * time.Second):
				logger.Info("sse", userID, sessionID, "agent goroutine stop timeout", time.Since(start))
			}
			return
		}
	}
}

// ----- sseWriter：实现 agent.StreamWriter -----

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex // 保护并发写入：agent goroutine 与心跳 ticker 都会写 w
}

// writeSSE 将 OutMsg 序列化为 JSON 后以 SSE 格式写入响应流并立即 Flush
func (s *sseWriter) writeSSE(msg OutMsg) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = fmt.Fprintf(s.w, "data: %s\n\n", string(b)); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// writeHeartbeat 发送 SSE 注释行作为心跳，保持连接存活
func (s *sseWriter) writeHeartbeat() {
	s.mu.Lock()
	fmt.Fprintf(s.w, ": heartbeat\n\n")
	s.flusher.Flush()
	s.mu.Unlock()
}

// WriteText 流式发送 LLM 文字 chunk
func (s *sseWriter) WriteText(text string) error {
	return s.writeSSE(OutMsg{Type: "text", Content: text})
}

// WriteProgress 推送执行进度事件，前端可实时展示当前步骤
func (s *sseWriter) WriteProgress(step, detail string, elapsedMs int64) error {
	return s.writeSSE(OutMsg{
		Type:      "progress",
		Step:      step,
		Detail:    detail,
		ElapsedMs: elapsedMs,
	})
}

// WriteInteractive 发送交互控件事件，前端按 kind 渲染按钮组/确认框等
func (s *sseWriter) WriteInteractive(kind string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.writeSSE(OutMsg{Type: "interactive", Step: kind, Data: b})
}

// WriteSpeaker 通知前端当前活跃技能的优雅名称
func (s *sseWriter) WriteSpeaker(name string) error {
	return s.writeSSE(OutMsg{Type: "speaker", Content: name})
}

// WriteImage 推送图片 Web 路径，前端内联渲染（如 <img src="/output/3/abc.png">）
func (s *sseWriter) WriteImage(url string) error {
	return s.writeSSE(OutMsg{Type: "image", Content: url})
}

// WriteEnd 发送结束事件
func (s *sseWriter) WriteEnd() error {
	return s.writeSSE(OutMsg{Type: "end"})
}

// WriteError 发送错误事件
func (s *sseWriter) WriteError(msg string) error {
	return s.writeSSE(OutMsg{Type: "error", Content: msg})
}
