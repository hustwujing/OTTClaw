// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/push/writer.go — CronWriter 实现 agent.StreamWriter 接口
// 将 agent 输出实时推送到 push.Default（供前端 /api/notify 消费）
// 不直接导入 agent 包（避免循环依赖），依靠 Go 结构化类型隐式满足接口
package push

import "encoding/json"

// CronWriter 将 agent 输出事件发布到 push.Default 的指定 session
type CronWriter struct {
	sessionID string
}

// NewCronWriter 创建 CronWriter 并立即发布 cron_start 事件通知前端
// jobMessage 是触发 cron 任务的用户消息原文，jobName 是任务名称
func NewCronWriter(sessionID, jobName, jobMessage string) *CronWriter {
	w := &CronWriter{sessionID: sessionID}
	w.publish(map[string]any{
		"type":    "cron_start",
		"content": jobMessage,
		"step":    jobName,
	})
	return w
}

// NewCronWriterSilent 创建 CronWriter 并发布 notify_start 事件（无用户气泡）。
// 用于 notifyParent / notifyMidTask：系统内部触发的 LLM 轮次，
// 前端需初始化 AI 气泡以接收后续 text/progress 事件，但不应显示用户侧气泡。
func NewCronWriterSilent(sessionID string) *CronWriter {
	w := &CronWriter{sessionID: sessionID}
	w.publish(map[string]any{"type": "notify_start"})
	return w
}

func (w *CronWriter) publish(m map[string]any) {
	b, _ := json.Marshal(m)
	Default.Publish(w.sessionID, b)
}

func (w *CronWriter) WriteText(text string) error {
	w.publish(map[string]any{"type": "text", "content": text})
	return nil
}

func (w *CronWriter) WriteProgress(step, detail, callID string, elapsedMs int64) error {
	m := map[string]any{
		"type":       "progress",
		"step":       step,
		"detail":     detail,
		"elapsed_ms": elapsedMs,
	}
	if callID != "" {
		m["call_id"] = callID
	}
	w.publish(m)
	return nil
}

// WriteInteractive 将交互控件事件（exec 确认框等）发布到前端 notify SSE 通道。
// notifyBatch/notifyParent 触发的父 agent 轮次由用户实时可见，因此需要支持交互。
func (w *CronWriter) WriteInteractive(kind string, data any) error {
	b, _ := json.Marshal(data)
	w.publish(map[string]any{
		"type": "interactive",
		"step": kind,
		"data": json.RawMessage(b),
	})
	return nil
}

func (w *CronWriter) WriteSpeaker(name string) error {
	w.publish(map[string]any{"type": "speaker", "content": name})
	return nil
}

func (w *CronWriter) WriteImage(url string) error {
	w.publish(map[string]any{"type": "image", "content": url})
	return nil
}

func (w *CronWriter) WriteEnd() error {
	w.publish(map[string]any{"type": "end"})
	return nil
}

func (w *CronWriter) WriteError(msg string) error {
	w.publish(map[string]any{"type": "error", "content": msg})
	return nil
}
