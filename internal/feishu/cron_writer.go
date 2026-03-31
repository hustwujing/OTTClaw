// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/feishu/cron_writer.go — 飞书场景的定时任务 StreamWriter
//
// 定时任务启动时向用户对话发送一张"执行中"卡片；
// 结束时将最终结果更新到同一张卡片，与 feishuWriter 的"一次性发送"风格保持一致。
// 不推送中间进度（cron 是 fire-and-forget，只需交付最终结果）。
// 若飞书 API 调用失败，静默降级（不影响任务执行和 DB 写回）。
package feishu

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"OTTClaw/internal/logger"
)

// FeishuCronWriter 实现 StreamWriter，将定时任务最终结果以主动消息推送到飞书对话。
type FeishuCronWriter struct {
	appID         string
	peer          string
	receiveIDType string
	ownerUserID   string
	jobName       string
	startTime     time.Time

	mu            sync.Mutex
	progressMsgID string          // 初始卡片的 message_id，用于 PATCH 更新
	textBuf       strings.Builder // 收集 LLM 文字输出，供 WriteEnd 发送
	finalized     bool            // 防止 WriteEnd / WriteError 重复执行
}

// NewCronWriter 创建 FeishuCronWriter，并立即向用户对话发送初始"执行中"卡片。
// 若发送失败，仍返回 writer（WriteEnd/WriteError 时降级为发送新消息）。
func NewCronWriter(ownerUserID, peer, appID, receiveIDType, jobName string) *FeishuCronWriter {
	w := &FeishuCronWriter{
		appID:         appID,
		peer:          peer,
		receiveIDType: receiveIDType,
		ownerUserID:   ownerUserID,
		jobName:       jobName,
		startTime:     time.Now(),
	}
	initialText := fmt.Sprintf("⏰ **定时任务执行中**\n%s\n\n*正在后台运行…*", jobName)
	msgID, err := SendCardGetID(appID, peer, receiveIDType, initialText)
	if err != nil {
		logger.Warn("feishu-cron", ownerUserID, "",
			fmt.Sprintf("send cron card: %v", err), 0)
	} else {
		w.progressMsgID = msgID
	}
	return w
}

// WriteText 累积 LLM 文字输出。
func (w *FeishuCronWriter) WriteText(text string) error {
	w.mu.Lock()
	w.textBuf.WriteString(text)
	w.mu.Unlock()
	return nil
}

// WriteProgress 不推送中间进度：定时任务只交付最终结果。
func (w *FeishuCronWriter) WriteProgress(_, _, _ string, _ int64) error { return nil }

// WriteInteractive 定时任务后台无人值守，静默忽略。
func (w *FeishuCronWriter) WriteInteractive(_ string, _ any) error { return nil }

// WriteSpeaker 静默忽略。
func (w *FeishuCronWriter) WriteSpeaker(_ string) error { return nil }

// WriteImage 上传图片并发送图片消息（独立消息，不更新进度卡片）。
func (w *FeishuCronWriter) WriteImage(url string) error {
	localPath, err := filepath.Abs(strings.TrimPrefix(url, "/"))
	if err != nil {
		return err
	}
	imageKey, err := UploadImage(w.appID, localPath)
	if err != nil {
		return fmt.Errorf("feishu upload cron image: %w", err)
	}
	return SendImageTo(w.appID, w.peer, w.receiveIDType, imageKey)
}

// WriteEnd 将完整 LLM 输出更新到初始卡片作为最终回复。
func (w *FeishuCronWriter) WriteEnd() error {
	w.mu.Lock()
	text := w.textBuf.String()
	w.mu.Unlock()
	w.sendFinal(text, false)
	return nil
}

// WriteError 将错误信息更新到初始卡片。
func (w *FeishuCronWriter) WriteError(msg string) error {
	w.sendFinal(msg, true)
	return nil
}

// sendFinal 幂等：只执行一次，重复调用无效。
func (w *FeishuCronWriter) sendFinal(text string, isError bool) {
	w.mu.Lock()
	if w.finalized {
		w.mu.Unlock()
		return
	}
	w.finalized = true
	msgID := w.progressMsgID
	w.mu.Unlock()

	// 移除飞书不支持的图片 Markdown 语法
	text = strings.TrimSpace(mdImageRe.ReplaceAllString(text, ""))
	elapsed := time.Since(w.startTime).Round(time.Second)

	var finalText string
	if isError {
		if text == "" {
			text = "未知错误"
		}
		finalText = fmt.Sprintf("❌ **定时任务失败** (%s)\n%s\n\n%s", elapsed, w.jobName, text)
	} else {
		if text == "" {
			text = "✅ 任务已完成"
		}
		finalText = fmt.Sprintf("✅ **定时任务完成** (%s)\n%s\n\n%s", elapsed, w.jobName, text)
	}

	if msgID != "" {
		if err := UpdateCard(w.appID, msgID, finalText); err != nil {
			logger.Warn("feishu-cron", w.ownerUserID, "",
				fmt.Sprintf("update cron card: %v", err), 0)
			// 降级：发送新消息
			_ = SendTextTo(w.appID, w.peer, w.receiveIDType, finalText)
		}
	} else {
		_ = SendTextTo(w.appID, w.peer, w.receiveIDType, finalText)
	}
}

// 编译期检查：FeishuCronWriter 必须实现 StreamWriter 接口。
var _ StreamWriter = (*FeishuCronWriter)(nil)
