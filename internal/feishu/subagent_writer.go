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

	"OTTClaw/internal/logger"
)

// subagentProgressInterval 进度卡片更新的最小间隔，防止超出飞书 API 频率限制。
const subagentProgressInterval = 3 * time.Second

// subagentSpinnerFrames 子任务进度卡片标题 spinner 帧序列。
// 每次更新卡片时轮换一帧，在飞书不支持 CSS 动画的环境下模拟"任务进行中"的视觉效果。
var subagentSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// FeishuSubagentWriter 实现 StreamWriter，将子 agent 执行进度以主动消息推送到飞书对话。
type FeishuSubagentWriter struct {
	appID         string
	peer          string
	receiveIDType string
	ownerUserID   string
	taskDesc      string // 截断后的任务摘要，显示在进度卡片标题
	startTime     time.Time
	notifyPolicy  string // 通知策略（done_only / state_changes / silent），影响 sendFinal 内容长度

	mu               sync.Mutex
	progressMsgID    string          // 进度卡片的 message_id，用于 PATCH 更新
	textBuf          strings.Builder // 收集 LLM 文字输出，供 sendFinal 发送最终结果
	finalized        bool            // 防止 WriteEnd / WriteError 重复执行
	lastUpdateAt     time.Time       // 上次更新进度卡片的时间（节流）
	lastDetail       string          // 上次展示的进度描述
	spinnerIdx       int             // 当前 spinner 帧索引，每次更新进度卡片时递增
	pendingImageKeys []string        // 已上传待发送的图片 image_key，sendFinal 后统一发出
	milestones       []string        // report_task_progress 上报的里程碑列表，追加到最终完成卡片
}

// NewSubagentWriter 创建 FeishuSubagentWriter，并立即向用户对话发送初始进度卡片。
// 若发送初始卡片失败，仍返回 writer（后续调用将直接发新消息作为降级）。
// notifyPolicy 影响 sendFinal：非 silent 模式时截断卡片内容以避免与父 Agent 回复重叠。
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

	// 发送初始进度卡片
	initialText := fmt.Sprintf("⚙ **子任务执行中**\n%s\n\n*启动中…*", desc)
	msgID, err := SendCardGetID(appID, peer, receiveIDType, initialText)
	if err != nil {
		logger.Warn("feishu-subagent", ownerUserID, "",
			fmt.Sprintf("send progress card: %v", err), 0)
	} else {
		w.progressMsgID = msgID
	}
	return w
}

// updateProgressCard 节流更新进度卡片，至多每 subagentProgressInterval 更新一次。
// 需在 mu 锁外调用（内部不加锁）。
func (w *FeishuSubagentWriter) updateProgressCard(detail string) {
	w.mu.Lock()
	if w.finalized {
		w.mu.Unlock()
		return
	}
	if time.Since(w.lastUpdateAt) < subagentProgressInterval {
		w.mu.Unlock()
		return
	}
	msgID := w.progressMsgID
	elapsed := time.Since(w.startTime).Round(time.Second)
	w.lastUpdateAt = time.Now()
	w.lastDetail = detail
	spinner := subagentSpinnerFrames[w.spinnerIdx%len(subagentSpinnerFrames)]
	w.spinnerIdx++
	w.mu.Unlock()

	if msgID == "" {
		return // 初始卡片发送失败，跳过进度更新
	}
	text := fmt.Sprintf("%s **子任务执行中** (%s)\n%s\n\n`%s`",
		spinner, elapsed, w.taskDesc, truncate(detail, 120))
	if err := UpdateCard(w.appID, msgID, text); err != nil {
		logger.Warn("feishu-subagent", w.ownerUserID, "",
			fmt.Sprintf("update progress card: %v", err), 0)
	}
}

// UpdateMilestone 将 report_task_progress 里程碑直接更新到进度卡片（绕过节流），
// 并将里程碑追加到 milestones 列表，供 sendFinal 在最终完成卡片中展示执行日志。
func (w *FeishuSubagentWriter) UpdateMilestone(progress string) {
	w.mu.Lock()
	if w.finalized {
		w.mu.Unlock()
		return
	}
	msgID := w.progressMsgID
	elapsed := time.Since(w.startTime).Round(time.Second)
	w.lastUpdateAt = time.Now() // 重置节流计时，避免立即被工具步骤更新覆盖
	spinner := subagentSpinnerFrames[w.spinnerIdx%len(subagentSpinnerFrames)]
	w.spinnerIdx++
	w.milestones = append(w.milestones, progress) // 保留里程碑，供 sendFinal 展示
	w.mu.Unlock()

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

// sendFinal 将最终内容更新到进度卡片（与 feishuWriter.sendFinal 逻辑一致），
// 并在卡片之后逐条发出执行期间积压的图片消息，保证图片紧跟完成卡片出现。
// 幂等：只执行一次，重复调用无效。
func (w *FeishuSubagentWriter) sendFinal(text string) {
	w.mu.Lock()
	if w.finalized {
		w.mu.Unlock()
		return
	}
	w.finalized = true
	msgID := w.progressMsgID
	imageKeys := append([]string(nil), w.pendingImageKeys...)   // 复制，锁外发送
	milestones := append([]string(nil), w.milestones...)        // 复制，锁外使用
	w.mu.Unlock()

	// 移除飞书不支持的图片 Markdown 语法
	text = strings.TrimSpace(mdImageRe.ReplaceAllString(text, ""))
	if text == "" {
		text = "✅ 子任务已完成"
	}

	// 非 silent 模式：父 Agent 会通过 RunForSession 处理完整结果并回复，
	// 卡片仅展示预览（≤400 字），避免与父回复内容重叠。
	// silent 模式：卡片是唯一输出渠道，展示完整内容。
	const cardPreviewLimit = 400
	displayText := text
	if w.notifyPolicy != "silent" && len([]rune(text)) > cardPreviewLimit {
		displayText = string([]rune(text)[:cardPreviewLimit]) + "\n…（完整结果已通知父 Agent）"
	}

	elapsed := time.Since(w.startTime).Round(time.Second)
	finalText := fmt.Sprintf("✅ **子任务完成** (%s)\n%s\n\n%s", elapsed, w.taskDesc, displayText)

	// 若执行期间有 report_task_progress 里程碑，以执行日志形式追加到卡片末尾，
	// 让用户在任务完成后仍能看到执行过程中的关键进度节点。
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
			// 降级：发送新消息
			_ = SendTextTo(w.appID, w.peer, w.receiveIDType, finalText)
		}
	} else {
		_ = SendTextTo(w.appID, w.peer, w.receiveIDType, finalText)
	}

	// 在完成卡片之后逐条发出积压图片，保证图片紧随完成状态出现，不被其他消息冲散
	for _, key := range imageKeys {
		if err := SendImageTo(w.appID, w.peer, w.receiveIDType, key); err != nil {
			logger.Warn("feishu-subagent", w.ownerUserID, "",
				fmt.Sprintf("send pending image: %v", err), 0)
		}
	}
}

// WriteText 累积 LLM 文字输出。
func (w *FeishuSubagentWriter) WriteText(text string) error {
	w.mu.Lock()
	w.textBuf.WriteString(text)
	w.mu.Unlock()
	return nil
}

// WriteProgress 节流更新进度卡片，展示当前工具调用等步骤信息。
func (w *FeishuSubagentWriter) WriteProgress(step, detail, _ string, _ int64) error {
	// 只展示有语义的步骤（工具调用/完成/错误），忽略纯 UI 步骤
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
// 图片不在执行期间立即发出，而是在 sendFinal 更新完成卡片后统一发送，
// 保证图片始终紧跟完成状态卡片出现，避免被其他消息冲散。
func (w *FeishuSubagentWriter) WriteImage(url string) error {
	localPath, err := filepath.Abs(strings.TrimPrefix(url, "/"))
	if err != nil {
		return err
	}
	imageKey, err := UploadImage(w.appID, localPath)
	if err != nil {
		return fmt.Errorf("feishu upload image: %w", err)
	}
	w.mu.Lock()
	w.pendingImageKeys = append(w.pendingImageKeys, imageKey)
	w.mu.Unlock()
	return nil
}

// WriteEnd 将完整 LLM 输出更新到进度卡片作为最终回复。
func (w *FeishuSubagentWriter) WriteEnd() error {
	w.mu.Lock()
	text := w.textBuf.String()
	w.mu.Unlock()
	w.sendFinal(text)
	return nil
}

// WriteError 将错误信息更新到进度卡片，并发出执行期间已上传的图片。
func (w *FeishuSubagentWriter) WriteError(msg string) error {
	w.mu.Lock()
	if w.finalized {
		w.mu.Unlock()
		return nil
	}
	w.finalized = true
	msgID := w.progressMsgID
	elapsed := time.Since(w.startTime).Round(time.Second)
	imageKeys := append([]string(nil), w.pendingImageKeys...)
	w.mu.Unlock()

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
// 飞书不支持 ![alt](url) 图片 Markdown 语法，图片已通过 sendFinal 以原生消息发出；
// 这里移除文本中残留的图片 Markdown（防御性处理），避免飞书将其显示为原始文本。
// 父 agent 通过末尾附加的说明感知图片已发送，不会重复生成。
func (w *FeishuSubagentWriter) TextResult() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	text := strings.TrimSpace(mdImageRe.ReplaceAllString(w.textBuf.String(), ""))
	if n := len(w.pendingImageKeys); n > 0 {
		text += fmt.Sprintf("\n\n[%d 张图片已自动发送给用户，无需重新生成]", n)
	}
	return text
}

// 编译期检查：FeishuSubagentWriter 必须实现本包定义的 StreamWriter 接口。
var _ StreamWriter = (*FeishuSubagentWriter)(nil)
