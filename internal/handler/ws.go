// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/handler/ws.go — WebSocket /ws?session_id=xxx&token=xxx
// 支持持久连接，每条用户消息触发一轮 Agent LLM 循环，流式返回回答
package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"OTTClaw/internal/agent"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/middleware"
)

// wsUpgrader 将 HTTP 连接升级为 WebSocket
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	// 生产环境应做 Origin 校验，此处允许所有来源
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ----- WebSocket 消息协议 -----

// wsInMsg 客户端发来的消息
type wsInMsg struct {
	Message string `json:"message"`
}

// WS 处理 WebSocket 连接
// 中间件 JWTAuth + SessionOwner 已在路由层挂载
func WS(c *gin.Context) {
	start := time.Now()
	userID := middleware.GetUserID(c)
	sessionID := middleware.GetSessionID(c)

	// 升级 HTTP 为 WebSocket
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Error("ws", userID, sessionID, "websocket upgrade failed", err, time.Since(start))
		return
	}
	defer func() {
		conn.Close()
		logger.Info("ws", userID, sessionID, "websocket connection closed", time.Since(start))
	}()

	logger.Info("ws", userID, sessionID, "websocket connected", time.Since(start))

	// 服务器关闭时，主动发 Close frame 并关闭连接，使阻塞中的 ReadJSON 立即返回错误退出。
	// WebSocket 是 hijacked 连接，srv.Shutdown() 不等待它，但仍需主动关闭避免 goroutine 泄漏。
	go func() {
		<-agent.Get().ShutdownCh()
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"))
		conn.Close()
	}()

	// 循环读取客户端消息
	for {
		var in wsInMsg
		if err := conn.ReadJSON(&in); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				logger.Error("ws", userID, sessionID, "ws read error", err, 0)
			} else {
				logger.Info("ws", userID, sessionID, "ws client disconnected", 0)
			}
			return
		}

		if in.Message == "" {
			_ = conn.WriteJSON(OutMsg{Type: "error", Content: "empty message"})
			continue
		}

		logger.Info("ws", userID, sessionID, "received user message", 0)

		// 创建 WebSocket 流式写入器
		writer := &wsWriter{conn: conn}

		// 运行 Agent 循环（阻塞直到 LLM 响应完成）
		if err := agent.Get().Run(c.Request.Context(), userID, sessionID, in.Message, writer); err != nil {
			logger.Error("ws", userID, sessionID, "agent run failed", err, 0)
			// writer.WriteError 已在 agent 内调用，此处不重复发送
		}
	}
}

// ----- wsWriter：实现 agent.StreamWriter -----

type wsWriter struct {
	conn *websocket.Conn
}

// WriteText 流式发送 LLM 文字 chunk
func (w *wsWriter) WriteText(text string) error {
	return w.conn.WriteJSON(OutMsg{Type: "text", Content: text})
}

// WriteProgress 推送执行进度事件，前端可实时展示当前步骤
func (w *wsWriter) WriteProgress(step, detail string, elapsedMs int64) error {
	return w.conn.WriteJSON(OutMsg{
		Type:      "progress",
		Step:      step,
		Detail:    detail,
		ElapsedMs: elapsedMs,
	})
}

// WriteInteractive 发送交互控件事件，前端按 kind 渲染按钮组/确认框等
func (w *wsWriter) WriteInteractive(kind string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return w.conn.WriteJSON(OutMsg{Type: "interactive", Step: kind, Data: b})
}

// WriteSpeaker 通知前端当前活跃技能的优雅名称
func (w *wsWriter) WriteSpeaker(name string) error {
	return w.conn.WriteJSON(OutMsg{Type: "speaker", Content: name})
}

// WriteImage 推送图片 Web 路径，前端内联渲染
func (w *wsWriter) WriteImage(url string) error {
	return w.conn.WriteJSON(OutMsg{Type: "image", Content: url})
}

// WriteEnd 发送结束信号
func (w *wsWriter) WriteEnd() error {
	return w.conn.WriteJSON(OutMsg{Type: "end"})
}

// WriteError 发送错误信息
func (w *wsWriter) WriteError(msg string) error {
	return w.conn.WriteJSON(OutMsg{Type: "error", Content: msg})
}
