// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

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

func (w *CronWriter) publish(m map[string]any) {
	b, _ := json.Marshal(m)
	Default.Publish(w.sessionID, b)
}

func (w *CronWriter) WriteText(text string) error {
	w.publish(map[string]any{"type": "text", "content": text})
	return nil
}

func (w *CronWriter) WriteProgress(step, detail string, elapsedMs int64) error {
	w.publish(map[string]any{
		"type":       "progress",
		"step":       step,
		"detail":     detail,
		"elapsed_ms": elapsedMs,
	})
	return nil
}

// WriteInteractive cron 任务不应有交互控件，静默忽略
func (w *CronWriter) WriteInteractive(_ string, _ any) error { return nil }

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
