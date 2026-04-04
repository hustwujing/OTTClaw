// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/feishu/subagent_writer.go — 飞书场景的子 agent StreamWriter（方案 C）
//
// 子 agent 启动时向用户对话发送一张进度卡片；执行过程中节流更新卡片展示当前步骤；
// 结束时将最终结果更新到同一张卡片，与 feishuWriter 的"一次性发送"风格保持一致。
// 若飞书 API 调用失败，静默降级（不影响子 agent 的正常执行和 DB 写回）。
package feishu

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"OTTClaw/internal/channel"
	"OTTClaw/internal/logger"
)

// subagentProgressInterval 进度卡片更新的最小间隔，防止超出飞书 API 频率限制。
const subagentProgressInterval = 3 * time.Second

// subagentSpinnerFrames 子任务进度卡片标题 spinner 帧序列。
var subagentSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// FeishuSubagentWriter 实现 channel.StreamWriter，将子 agent 执行进度以主动消息推送到飞书对话。
type FeishuSubagentWriter struct {
	channel.BaseWriter // textBuf, finalized, WriteText, WriteEnd, WriteError, Close

	appID         string
	peer          string
	receiveIDType string
	ownerUserID   string
	taskDesc      string // 截断后的任务摘要，显示在进度卡片标题
	startTime     time.Time
	notifyPolicy  string

	smw              sync.Mutex // 保护飞书专属可变状态
	progressMsgID    string
	lastUpdateAt     time.Time
	lastDetail       string
	spinnerIdx       int
	pendingImageKeys []string
	milestones       []string
}

// NewSubagentWriter 创建 FeishuSubagentWriter，并立即向用户对话发送初始进度卡片。
func NewSubagentWriter(ownerUserID, peer, appID, receiveIDType, taskDesc, notifyPolicy string) *FeishuSubagentWriter {
	desc := taskDesc
	if len([]rune(desc)) > 40 {
		runes := []rune(desc)
		desc = string(runes[:40]) + "…"
	}

	w := &FeishuSubagentWriter{
		appID:         appID,
		peer:          peer,
		receiveIDType: receiveIDType,
		ownerUserID:   ownerUserID,
		taskDesc:      desc,
		startTime:     time.Now(),
		notifyPolicy:  notifyPolicy,
	}
	w.BaseWriter.OwnerUserID = ownerUserID

	initialText := fmt.Sprintf("⚙ **子任务执行中**\n%s\n\n*启动中…*", desc)
	msgID, err := SendCardGetID(appID, peer, receiveIDType, initialText)
	if err != nil {
		logger.Warn("feishu-subagent", ownerUserID, "",
			fmt.Sprintf("send progress card: %v", err), 0)
	} else {
		w.progressMsgID = msgID
	}

	// 注入 SendFn：最终完成时的卡片更新逻辑
	w.BaseWriter.SendFn = w.doSendFinal
	return w
}

// doSendFinal 是注入到 BaseWriter.SendFn 的最终发送逻辑（WriteEnd 时调用）
func (w *FeishuSubagentWriter) doSendFinal(text string) {
	w.smw.Lock()
	msgID := w.progressMsgID
	imageKeys := append([]string(nil), w.pendingImageKeys...)
	milestones := append([]string(nil), w.milestones...)
	w.smw.Unlock()

	// 移除飞书不支持的图片 Markdown 语法
	text = strings.TrimSpace(mdImageRe.ReplaceAllString(text, ""))
	if text == "" || text == "✅ 已完成" {
		text = "✅ 子任务已完成"
	}

	// 非 silent 模式：父 Agent 会通过 RunForSession 处理完整结果并回复，
	// 卡片仅展示预览（≤400 字），避免与父回复内容重叠。
	const cardPreviewLimit = 400
	displayText := text
	if w.notifyPolicy != "silent" && len([]rune(text)) > cardPreviewLimit {
		displayText = string([]rune(text)[:cardPreviewLimit]) + "\n…（完整结果已通知父 Agent）"
	}

	elapsed := time.Since(w.startTime).Round(time.Second)
	finalText := fmt.Sprintf("✅ **子任务完成** (%s)\n%s\n\n%s", elapsed, w.taskDesc, displayText)

	if len(milestones) > 0 {
		var logLines strings.Builder
		logLines.WriteString("\n\n**📋 执行日志**")
		for _, m := range milestones {
			logLines.WriteString("\n· ")
			logLines.WriteString(truncate(m, 100))
		}
		finalText += logLines.String()
	}

	if msgID != "" {
		if err := UpdateCard(w.appID, msgID, finalText); err != nil {
			logger.Warn("feishu-subagent", w.ownerUserID, "",
				fmt.Sprintf("update final card: %v", err), 0)
			_ = SendTextTo(w.appID, w.peer, w.receiveIDType, finalText)
		}
	} else {
		_ = SendTextTo(w.appID, w.peer, w.receiveIDType, finalText)
	}

	// 在完成卡片之后逐条发出积压图片
	for _, key := range imageKeys {
		if err := SendImageTo(w.appID, w.peer, w.receiveIDType, key); err != nil {
			logger.Warn("feishu-subagent", w.ownerUserID, "",
				fmt.Sprintf("send pending image: %v", err), 0)
		}
	}
}

// updateProgressCard 节流更新进度卡片，至多每 subagentProgressInterval 更新一次。
func (w *FeishuSubagentWriter) updateProgressCard(detail string) {
	if w.IsFinalized() {
		return
	}
	w.smw.Lock()
	if time.Since(w.lastUpdateAt) < subagentProgressInterval {
		w.smw.Unlock()
		return
	}
	msgID := w.progressMsgID
	elapsed := time.Since(w.startTime).Round(time.Second)
	w.lastUpdateAt = time.Now()
	w.lastDetail = detail
	spinner := subagentSpinnerFrames[w.spinnerIdx%len(subagentSpinnerFrames)]
	w.spinnerIdx++
	w.smw.Unlock()

	if msgID == "" {
		return
	}
	text := fmt.Sprintf("%s **子任务执行中** (%s)\n%s\n\n`%s`",
		spinner, elapsed, w.taskDesc, truncate(detail, 120))
	if err := UpdateCard(w.appID, msgID, text); err != nil {
		logger.Warn("feishu-subagent", w.ownerUserID, "",
			fmt.Sprintf("update progress card: %v", err), 0)
	}
}

// UpdateMilestone 将 report_task_progress 里程碑直接更新到进度卡片（绕过节流）。
func (w *FeishuSubagentWriter) UpdateMilestone(progress string) {
	if w.IsFinalized() {
		return
	}
	w.smw.Lock()
	msgID := w.progressMsgID
	elapsed := time.Since(w.startTime).Round(time.Second)
	w.lastUpdateAt = time.Now()
	spinner := subagentSpinnerFrames[w.spinnerIdx%len(subagentSpinnerFrames)]
	w.spinnerIdx++
	w.milestones = append(w.milestones, progress)
	w.smw.Unlock()

	if msgID == "" {
		return
	}
	text := fmt.Sprintf("%s **子任务执行中** (%s)\n%s\n\n📊 %s",
		spinner, elapsed, w.taskDesc, truncate(progress, 160))
	if err := UpdateCard(w.appID, msgID, text); err != nil {
		logger.Warn("feishu-subagent", w.ownerUserID, "",
			fmt.Sprintf("update milestone card: %v", err), 0)
	}
}

// WriteProgress 节流更新进度卡片，展示当前工具调用等步骤信息。
func (w *FeishuSubagentWriter) WriteProgress(step, detail, _ string, _ int64) error {
	if step == "tool_call" || step == "tool_done" || step == "tool_error" {
		label := map[string]string{
			"tool_call":  "调用工具",
			"tool_done":  "工具完成",
			"tool_error": "工具失败",
		}[step]
		w.updateProgressCard(label + ": " + detail)
	}
	return nil
}

// WriteInteractive 子 agent 后台运行，无交互，静默忽略。
func (w *FeishuSubagentWriter) WriteInteractive(_ string, _ any) error { return nil }

// WriteSpeaker 静默忽略。
func (w *FeishuSubagentWriter) WriteSpeaker(_ string) error { return nil }

// WriteImage 上传图片并将 image_key 存入待发队列。
func (w *FeishuSubagentWriter) WriteImage(url string) error {
	localPath, err := filepath.Abs(strings.TrimPrefix(url, "/"))
	if err != nil {
		return err
	}
	imageKey, err := UploadImage(w.appID, localPath)
	if err != nil {
		return fmt.Errorf("feishu upload image: %w", err)
	}
	w.smw.Lock()
	w.pendingImageKeys = append(w.pendingImageKeys, imageKey)
	w.smw.Unlock()
	return nil
}

// WriteError 将错误信息更新到进度卡片，并发出执行期间已上传的图片。
// 覆盖 BaseWriter.WriteError 以实现飞书专属的错误格式和图片发送。
func (w *FeishuSubagentWriter) WriteError(msg string) error {
	if !w.TryFinalize() {
		return nil
	}
	w.smw.Lock()
	msgID := w.progressMsgID
	elapsed := time.Since(w.startTime).Round(time.Second)
	imageKeys := append([]string(nil), w.pendingImageKeys...)
	w.smw.Unlock()

	errText := fmt.Sprintf("❌ **子任务失败** (%s)\n%s\n\n%s", elapsed, w.taskDesc, msg)
	if msgID != "" {
		if err := UpdateCard(w.appID, msgID, errText); err != nil {
			_ = SendTextTo(w.appID, w.peer, w.receiveIDType, errText)
		}
	} else {
		_ = SendTextTo(w.appID, w.peer, w.receiveIDType, errText)
	}
	for _, key := range imageKeys {
		if err := SendImageTo(w.appID, w.peer, w.receiveIDType, key); err != nil {
			logger.Warn("feishu-subagent", w.ownerUserID, "",
				fmt.Sprintf("send pending image on error: %v", err), 0)
		}
	}
	return nil
}

// truncate 截断字符串到指定字节数（中文安全）。
func truncate(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

// TextResult 返回子 agent 收集到的完整 LLM 文字输出，供 RunBackground 写入 sub_tasks.result。
func (w *FeishuSubagentWriter) TextResult() string {
	text := strings.TrimSpace(mdImageRe.ReplaceAllString(w.FlushText(), ""))
	w.smw.Lock()
	n := len(w.pendingImageKeys)
	w.smw.Unlock()
	if n > 0 {
		text += fmt.Sprintf("\n\n[%d 张图片已自动发送给用户，无需重新生成]", n)
	}
	return text
}

// 编译期检查：FeishuSubagentWriter 必须实现 channel.StreamWriter 接口。
var _ channel.StreamWriter = (*FeishuSubagentWriter)(nil)
