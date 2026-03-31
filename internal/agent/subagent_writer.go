// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/agent/subagent_writer.go — 子 agent 实时进度推送 StreamWriter
//
// SubagentWriter 将子 agent 的执行事件实时发布到父 session 的 /api/notify 频道，
// 前端通过 subscribeNotify 收到后渲染折叠式子任务卡片。
// 每个事件携带 task_id，前端可区分多个并发子 agent。
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"OTTClaw/internal/push"
)

// SubagentWriter 实现 StreamWriter，将子 agent 事件推送到父会话的 notify 频道。
type SubagentWriter struct {
	parentSessionID string
	taskID          uint
	startTime       time.Time
	mu              sync.Mutex
	buf             strings.Builder // 收集 LLM 文字输出，供 Result() 写入 sub_tasks.result
	pendingImages   []string        // 收集图片 URL，供 result() 写入 sub_tasks.result
}

// NewSubagentWriter 创建 SubagentWriter 并立即发布 subagent_start 事件，
// 通知前端为该任务初始化折叠卡片。
// label 为可选的短标签（来自 spawn_subagent），空字符串表示无标签。
func NewSubagentWriter(parentSessionID string, taskID uint, taskDesc, label string) *SubagentWriter {
	w := &SubagentWriter{
		parentSessionID: parentSessionID,
		taskID:          taskID,
		startTime:       time.Now(),
	}
	w.publish(map[string]any{
		"type":    "subagent_start",
		"task_id": taskID,
		"content": taskDesc, // 完整任务描述，显示在折叠卡片展开区域
		"label":   label,    // 短标签，显示在折叠卡片标题；空字符串时前端降级显示 task_id
	})
	return w
}

func (w *SubagentWriter) publish(m map[string]any) {
	b, _ := json.Marshal(m)
	push.Default.Publish(w.parentSessionID, b)
}

func (w *SubagentWriter) elapsed() int64 {
	return time.Since(w.startTime).Milliseconds()
}

// WriteText 收集 LLM 文字输出并推送到前端（在折叠内容中展示）。
func (w *SubagentWriter) WriteText(text string) error {
	w.mu.Lock()
	w.buf.WriteString(text)
	w.mu.Unlock()
	w.publish(map[string]any{
		"type":       "subagent_text",
		"task_id":    w.taskID,
		"content":    text,
		"elapsed_ms": w.elapsed(),
	})
	return nil
}

// WriteProgress 将工具调用等进度事件推送到前端卡片的步骤列表。
func (w *SubagentWriter) WriteProgress(step, detail, callID string, elapsedMs int64) error {
	m := map[string]any{
		"type":       "subagent_progress",
		"task_id":    w.taskID,
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

// WriteInteractive 子 agent 后台无人值守，不支持交互，静默忽略。
func (w *SubagentWriter) WriteInteractive(_ string, _ any) error { return nil }

// WriteSpeaker 暂不展示子 agent 的 speaker 信息，静默忽略。
func (w *SubagentWriter) WriteSpeaker(_ string) error { return nil }

// WriteImage 将图片推送到卡片内容区（执行期间可见的缩略图），
// 并将 URL 加入队列，供 result() 写入 sub_tasks.result，让父 agent 在主气泡里渲染图片。
func (w *SubagentWriter) WriteImage(url string) error {
	w.publish(map[string]any{
		"type":       "subagent_image",
		"task_id":    w.taskID,
		"content":    url,
		"elapsed_ms": w.elapsed(),
	})
	w.mu.Lock()
	w.pendingImages = append(w.pendingImages, url)
	w.mu.Unlock()
	return nil
}

// WriteEnd 发布 subagent_end 事件，前端将卡片折叠并标记完成。
// 图片已在执行期间通过 subagent_image 事件在卡片内展示，无需再发 subagent_image_final，
// 避免图片在主聊天流的子任务卡片之间额外出现。
func (w *SubagentWriter) WriteEnd() error {
	w.publish(map[string]any{
		"type":       "subagent_end",
		"task_id":    w.taskID,
		"elapsed_ms": w.elapsed(),
	})
	return nil
}

// WriteError 发布 subagent_error 事件，前端将卡片标记为失败。
func (w *SubagentWriter) WriteError(msg string) error {
	w.publish(map[string]any{
		"type":       "subagent_error",
		"task_id":    w.taskID,
		"content":    msg,
		"elapsed_ms": w.elapsed(),
	})
	return nil
}

// result 返回子 agent 产出的完整文字内容，供 RunBackground 写入 sub_tasks.result。
// 若执行期间有图片通过 WriteImage 注册并发送：
//   - 将图片 URL 以 markdown 形式追加到 result（父 agent 看到后可在主气泡里渲染）
//   - 附加说明，告知父 agent 图片已发送，无需重新生成
//
// 注意：此内容仅写入 sub_tasks.result（DB），不经过 WriteText，
// 因此不会触发 subagent_text 事件，卡片内不会出现额外的图片渲染。
func (w *SubagentWriter) result() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	text := w.buf.String()
	if n := len(w.pendingImages); n > 0 {
		for _, url := range w.pendingImages {
			text += fmt.Sprintf("\n\n![图片](%s)", url)
		}
		text += fmt.Sprintf("\n\n[以上 %d 张图片是子任务的输出。请在你的回复中直接以 markdown 语法 ![图片](URL) 内联展示，不要说「图片已发送」，让图片直接显示在对话中。]", n)
	}
	return text
}
