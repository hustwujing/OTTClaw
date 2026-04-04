// internal/weixin/registry.go — 微信包级 Registry 引用 + weixinWriter + 活跃客户端管理
package weixin

import (
	"fmt"
	"strings"
	"sync"

	"OTTClaw/internal/channel"
	"OTTClaw/internal/logger"
)

var globalRegistry *channel.Registry

func SetRegistry(reg *channel.Registry) { globalRegistry = reg }
func GetRegistry() *channel.Registry    { return globalRegistry }

var (
	activeClientsMu sync.RWMutex
	activeClients   = map[string]*Client{}
)

func setActiveClient(userID string, c *Client) {
	activeClientsMu.Lock()
	activeClients[userID] = c
	activeClientsMu.Unlock()
}

func removeActiveClient(userID string) {
	activeClientsMu.Lock()
	delete(activeClients, userID)
	activeClientsMu.Unlock()
}

func GetActiveClient(userID string) *Client {
	activeClientsMu.RLock()
	defer activeClientsMu.RUnlock()
	return activeClients[userID]
}

func SetActiveClientForBind(userID string, c *Client) { setActiveClient(userID, c) }

// ── weixinWriter ────────────────────────────────────────────

type weixinWriter struct {
	channel.BaseWriter
	peerID       string
	contextToken string
	client       *Client
}

func newWeixinWriter(ownerUserID, sessionID, peerID, contextToken string, client *Client) *weixinWriter {
	w := &weixinWriter{peerID: peerID, contextToken: contextToken, client: client}
	w.BaseWriter.OwnerUserID = ownerUserID
	w.BaseWriter.SessionID = sessionID

	// 立即发送等待提示，让用户知道消息已收到（微信无法编辑消息，此气泡会保留）
	if err := client.SendText(peerID, "⏳ 让我先看看，别慌，小场面…", contextToken); err != nil {
		logger.Warn("weixin", ownerUserID, sessionID, fmt.Sprintf("send ack: %v", err), 0)
	}

	w.BaseWriter.SendFn = func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			text = "✅ 已完成"
		}
		if err := w.client.SendText(w.peerID, text, w.contextToken); err != nil {
			logger.Warn("weixin", w.OwnerUserID, w.SessionID, fmt.Sprintf("send reply error: %v", err), 0)
		}
	}
	return w
}

func (w *weixinWriter) WriteImage(url string) error {
	if w.client == nil {
		return fmt.Errorf("not connected")
	}
	if err := w.client.SendImage(w.peerID, url, w.contextToken); err != nil {
		logger.Warn("weixin", w.OwnerUserID, w.SessionID, fmt.Sprintf("send image error: %v", err), 0)
	}
	return nil
}

var _ channel.StreamWriter = (*weixinWriter)(nil)
