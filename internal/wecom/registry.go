// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/wecom/registry.go — 企业微信包级 Registry 引用 + wecomWriter
//
// Registry 生命周期由 channel.Registry 统一管理（见 adapter.go + main.go）。
// 此文件保留 wecom 包级 registry 引用（供 tool/wecom.go 热重启时调用）和 wecomWriter。
package wecom

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"OTTClaw/internal/channel"
	"OTTClaw/internal/logger"
)

// ── 包级 Registry 引用（供 tool 层热重启调用） ────────────────────────────────

var globalRegistry *channel.Registry

// SetRegistry 存储 channel.Registry 引用，供 tool/wecom.go 调用 StartForUser
func SetRegistry(reg *channel.Registry) {
	globalRegistry = reg
}

// GetRegistry 返回当前的 channel.Registry（nil 表示未初始化）
func GetRegistry() *channel.Registry {
	return globalRegistry
}

// ── wecomWriter ───────────────────────────────────────────────────────────────

// wecomWriter 实现 channel.StreamWriter。
// 利用企微 AI Bot 原生流式协议（msgtype:"stream"），每次 WriteText 立即推送
// finish=false 帧，WriteEnd 发送 finish=true 终止帧，实现真正的逐 token 实时输出。
type wecomWriter struct {
	channel.BaseWriter // textBuf, finalized, WriteText, WriteEnd, WriteError, Close

	reqID    string // 透传自企微消息帧
	streamID string // 本次回复的流式 ID
	client   *BotClient

	// sendMu 防止并发发送帧（BaseWriter.mu 保护 textBuf/finalized；sendMu 保护网络写入和 seq）
	sendMu sync.Mutex
	seq    int // 已发送的流帧序号；0 表示尚未发送任何块
}

func newWeComWriter(ownerUserID, sessionID, reqID string, client *BotClient) *wecomWriter {
	w := &wecomWriter{
		reqID:    reqID,
		streamID: uuid.New().String(),
		client:   client,
	}
	w.BaseWriter.OwnerUserID = ownerUserID
	w.BaseWriter.SessionID = sessionID

	// SendFn 由 WriteEnd/WriteError 调用，此时所有 WriteText 块已推送完毕。
	w.BaseWriter.SendFn = func(text string) {
		text = strings.TrimSpace(text)
		w.sendMu.Lock()
		defer w.sendMu.Unlock()
		if w.seq == 0 {
			// WriteText 从未被调用（非流式路径），一次性发送全部文本并结束
			if text == "" {
				text = "✅ 已完成"
			}
			if err := w.client.SendReply(w.reqID, w.streamID, 1, text, true); err != nil {
				logger.Warn("wecom", ownerUserID, sessionID,
					fmt.Sprintf("send reply error: %v", err), 0)
			}
		} else {
			// 已有流式块在途，发送终止帧（content 留空，客户端以 finish=true 为结束标志）
			w.seq++
			if err := w.client.SendReply(w.reqID, w.streamID, w.seq, "", true); err != nil {
				logger.Warn("wecom", ownerUserID, sessionID,
					fmt.Sprintf("send final frame error: %v", err), 0)
			}
		}
	}
	return w
}

// WriteText 覆写 BaseWriter，将每个 LLM 输出块立即以 finish=false 帧推送给客户端。
func (w *wecomWriter) WriteText(text string) error {
	if text == "" {
		return nil
	}
	_ = w.BaseWriter.WriteText(text) // 仍累积到 textBuf，供 FlushText 等使用
	w.sendMu.Lock()
	defer w.sendMu.Unlock()
	w.seq++
	if err := w.client.SendReply(w.reqID, w.streamID, w.seq, text, false); err != nil {
		logger.Warn("wecom", w.OwnerUserID, w.SessionID,
			fmt.Sprintf("stream chunk %d: %v", w.seq, err), 0)
	}
	return nil
}

// WriteProgress、WriteSpeaker、WriteInteractive：企微暂不支持，静默处理
func (w *wecomWriter) WriteProgress(_, _, _ string, _ int64) error { return nil }
func (w *wecomWriter) WriteSpeaker(_ string) error                  { return nil }
func (w *wecomWriter) WriteInteractive(_ string, _ any) error       { return nil }

// WriteImage 上传本地图片并通过 AI Bot 发送给用户
// path 可为绝对路径，也可为 web 相对路径（如 /output/xxx.png），
// 后者会被转换为 {cwd}/output/xxx.png 的绝对路径。
func (w *wecomWriter) WriteImage(path string) error {
	if path == "" {
		return nil
	}
	// Web 路径（/output/xxx.png）→ 本地绝对路径（{cwd}/output/xxx.png）
	if strings.HasPrefix(path, "/") {
		if candidate, err := filepath.Abs(strings.TrimPrefix(path, "/")); err == nil {
			if _, statErr := os.Stat(candidate); statErr == nil {
				path = candidate
			}
		}
	}
	mediaID, err := w.client.UploadMedia(path)
	if err != nil {
		logger.Warn("wecom", w.BaseWriter.OwnerUserID, w.BaseWriter.SessionID,
			fmt.Sprintf("upload image error: %v", err), 0)
		return err
	}
	w.sendMu.Lock()
	defer w.sendMu.Unlock()
	return w.client.SendMediaReply(w.reqID, mediaID, "image")
}

// 编译期检查
var _ channel.StreamWriter = (*wecomWriter)(nil)
