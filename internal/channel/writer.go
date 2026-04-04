// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/channel/writer.go — BaseWriter：消除各 writer 中重复的 textBuf + finalized + sendFinal 模式
//
// 用法：
//
//	type myWriter struct {
//	    channel.BaseWriter
//	    // 渠道专属字段
//	}
//
//	func newMyWriter(...) *myWriter {
//	    w := &myWriter{...}
//	    w.BaseWriter.SendFn = func(text string) {
//	        // 渠道专属发送逻辑（此时 finalized 已置为 true）
//	    }
//	    return w
//	}
package channel

import (
	"strings"
	"sync"
)

// BaseWriter 提供 textBuf 累积、finalized 幂等保护和 SendFn 注入，
// 供各渠道 writer 嵌入，避免重复实现相同的缓冲 + 幂等发送模式。
type BaseWriter struct {
	OwnerUserID string
	SessionID   string
	// SendFn 由构造时注入，是渠道专属的最终发送逻辑；
	// 在 finalized 置为 true 后调用，保证幂等。
	SendFn func(text string)

	mu        sync.Mutex
	textBuf   strings.Builder
	finalized bool
}

// WriteText 累积 LLM 文字 chunk
func (w *BaseWriter) WriteText(text string) error {
	w.mu.Lock()
	w.textBuf.WriteString(text)
	w.mu.Unlock()
	return nil
}

// FlushText 读取已积累的文字（不修改 finalized 状态，不重置缓冲区）
func (w *BaseWriter) FlushText() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.textBuf.String()
}

// FlushAndResetText 读取已积累文字并清空缓冲区（原子操作，不修改 finalized）
func (w *BaseWriter) FlushAndResetText() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	text := w.textBuf.String()
	w.textBuf.Reset()
	return text
}

// TryFinalize 尝试将 finalized 从 false 置为 true（原子）。
// 返回 true 表示本次调用完成了状态转换；返回 false 表示已被其他调用抢先完成。
// 供 WriteInteractive 等需要自定义最终发送逻辑的方法使用。
func (w *BaseWriter) TryFinalize() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finalized {
		return false
	}
	w.finalized = true
	return true
}

// IsFinalized 返回是否已完成（供嵌入方判断）
func (w *BaseWriter) IsFinalized() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.finalized
}

// Finalize 幂等 sendFinal：只执行一次。
// text 为最终文本；空时传入 "✅ 已完成"。
func (w *BaseWriter) Finalize(text string) {
	if !w.TryFinalize() {
		return
	}
	if text == "" {
		text = "✅ 已完成"
	}
	if w.SendFn != nil {
		w.SendFn(text)
	}
}

// WriteEnd 将已积累的文字一次性发出（实现 StreamWriter.WriteEnd）
func (w *BaseWriter) WriteEnd() error {
	w.mu.Lock()
	text := w.textBuf.String()
	w.mu.Unlock()
	w.Finalize(text)
	return nil
}

// WriteError 发送错误提示（实现 StreamWriter.WriteError）
func (w *BaseWriter) WriteError(msg string) error {
	w.Finalize("❌ " + msg)
	return nil
}

// Close 是 defer 安全网：若 WriteEnd/WriteError 已完成则忽略
func (w *BaseWriter) Close() {
	w.mu.Lock()
	text := w.textBuf.String()
	w.mu.Unlock()
	w.Finalize(text)
}

// 默认 no-op 实现，嵌入方按需 override：

func (w *BaseWriter) WriteProgress(_, _, _ string, _ int64) error { return nil }
func (w *BaseWriter) WriteSpeaker(_ string) error                  { return nil }
func (w *BaseWriter) WriteImage(_ string) error                    { return nil }
func (w *BaseWriter) WriteInteractive(_ string, _ any) error       { return nil }
