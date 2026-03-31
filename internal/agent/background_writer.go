// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/agent/background_writer.go — 后台子 agent 使用的 StreamWriter 实现
// 收集 LLM 最终文字输出，丢弃进度/交互/图片等前端事件（后台运行无前端连接）。
package agent

import "sync"

// resultWriter 实现 StreamWriter 接口，用于后台子 agent 运行。
// WriteText 收集文字 chunk，其余事件静默丢弃。
type resultWriter struct {
	mu  sync.Mutex
	buf []string
}

func (w *resultWriter) WriteText(text string) error {
	w.mu.Lock()
	w.buf = append(w.buf, text)
	w.mu.Unlock()
	return nil
}

func (w *resultWriter) WriteProgress(_, _, _ string, _ int64) error { return nil }
func (w *resultWriter) WriteInteractive(_ string, _ any) error   { return nil }
func (w *resultWriter) WriteSpeaker(_ string) error              { return nil }
func (w *resultWriter) WriteImage(_ string) error                { return nil }
func (w *resultWriter) WriteEnd() error                          { return nil }
func (w *resultWriter) WriteError(_ string) error                { return nil }

// result 返回所有收到的文字 chunk 拼接后的完整文本。
func (w *resultWriter) result() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var s string
	for _, chunk := range w.buf {
		s += chunk
	}
	return s
}
