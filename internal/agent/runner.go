// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/agent/runner.go — 后台子 agent 任务执行
//
// RunBackground 在后台 goroutine 中运行完整的 agent 循环，完成后将结果写回 sub_tasks 表。
// 根据父会话来源自动选择合适的 StreamWriter：
//   - feishu 会话 → FeishuSubagentWriter（方案 C：主动消息推送）
//   - web 会话    → SubagentWriter（方案 B：通过 push.Default 推送到 /api/notify SSE）
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"OTTClaw/internal/feishu"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/push"
	"OTTClaw/internal/runtrack"
	"OTTClaw/internal/storage"
	"OTTClaw/internal/tool"
)

// subagentTimeout 单个子 agent 任务的最长运行时间。
// 超时后 context 被取消，LLM 调用中断，任务标记为 failed。
const subagentTimeout = 30 * time.Minute

// RunBackground 后台运行子 agent 任务。
// 应在独立 goroutine 中调用（由 spawn_subagent 工具触发）。
//
// taskID：sub_tasks 表主键；userID/childSessionID：子会话身份；
// taskDesc：传给子 agent 的任务描述；parentSessionID：父会话 ID，用于选择 writer。
func (a *Agent) RunBackground(taskID uint, userID, childSessionID, taskDesc, parentSessionID string) {
	// 读取任务的通知策略、标签和父任务 ID（创建时已写入）
	notifyPolicy := "done_only"
	label := ""
	parentTaskID := uint(0)
	if t, err := storage.GetSubTask(taskID); err == nil && t != nil {
		if t.NotifyPolicy != "" {
			notifyPolicy = t.NotifyPolicy
		}
		label = t.Label
		parentTaskID = t.ParentTaskID
	}

	// 标记任务为 running
	if err := storage.UpdateSubTaskStatus(taskID, "running", "", ""); err != nil {
		logger.Warn("subagent", userID, childSessionID,
			"failed to mark sub_task as running", 0)
	}

	logger.Info("subagent", userID, childSessionID,
		fmt.Sprintf("subagent started (notify_policy=%s)", notifyPolicy), 0)

	// state_changes：running 状态也通知父会话
	if notifyPolicy == "state_changes" {
		a.notifyStateChange(parentSessionID, userID, taskID, label, "running")
	}

	// 创建带超时的上下文，以 bgCtx 为父（服务关闭时一并取消）
	ctx, cancel := context.WithTimeout(a.bgCtx, subagentTimeout)
	// 注册取消函数，供 cancel_subtask 工具主动取消
	a.subTaskCancels.Store(taskID, cancel)
	defer func() {
		cancel()
		a.subTaskCancels.Delete(taskID)
	}()

	// 注入子 agent 自身 task ID、进度上报闭包及父会话通知函数。
	// sw 须在闭包之前声明（闭包在 a.Run 期间被调用，届时 sw 已赋值）。
	ctx = tool.WithTaskID(ctx, taskID)
	// notify_parent 工具：子 agent 中途向父 agent 注入消息，触发父 session 新 LLM turn。
	// 异步执行（独立 goroutine），不阻塞子任务继续运行。
	ctx = tool.WithParentNotifier(ctx, func(message string) {
		a.bgWg.Add(1)
		go func() {
			defer a.bgWg.Done()
			a.notifyMidTask(parentSessionID, userID, taskID, label, message)
		}()
	})
	var sw subagentWriterIface
	ctx = tool.WithSubtaskProgressReporter(ctx, func(progress string) {
		_ = storage.UpdateSubTaskProgress(taskID, progress)
		// 飞书：直接更新进度卡片，并将里程碑记录追加到最终完成卡片
		if fa, ok := sw.(*feishuSubagentAdapter); ok {
			fa.w.UpdateMilestone(progress)
			return
		}
		// web 及其他来源：始终推送 SSE 进度事件，与 notifyPolicy 无关。
		// notifyPolicy 只控制父 agent LLM 是否被唤醒（notifyBatch/notifyParent），
		// 不控制子任务卡片内的进度展示（用户可见性）。
		a.notifyProgressUpdate(parentSessionID, userID, taskID, label, progress)
	})

	// 根据父会话来源选择合适的 StreamWriter（notifyPolicy 传给飞书 writer 控制 sendFinal 截断）
	sw = a.newSubagentWriter(parentSessionID, taskID, taskDesc, label, notifyPolicy)

	defer runtrack.Default.Register("subagent", userID, childSessionID)()
	err := a.Run(ctx, userID, childSessionID, taskDesc, sw)

	if err != nil {
		if errors.Is(err, context.Canceled) {
			// force=true 路径下 DB 已被 ForceKillSubTask 立即写为 killed，跳过重复写入
			if t, e := storage.GetSubTask(taskID); e == nil && t != nil && t.Status == "killed" {
				logger.Info("subagent", userID, childSessionID,
					"subagent force-killed", 0)
				a.publishSubagentStatus(parentSessionID, taskID, "killed")
				// force-killed 也须触发批量通知，否则若此任务最后进入终态，批量通知永不触发
				a.maybeNotifyBatch(parentSessionID, userID, parentTaskID)
				return
			}
			cancelMsg := "任务已被主动取消"
			logger.Info("subagent", userID, childSessionID,
				"subagent cancelled", 0)
			_ = storage.UpdateSubTaskStatus(taskID, "cancelled", "", cancelMsg)
			a.publishSubagentStatus(parentSessionID, taskID, "cancelled")
			a.maybeNotifyBatch(parentSessionID, userID, parentTaskID)
		} else if errors.Is(err, context.DeadlineExceeded) {
			timeoutMsg := fmt.Sprintf("任务超时（超过 %v），已自动终止", subagentTimeout)
			logger.Warn("subagent", userID, childSessionID,
				"subagent timed out: "+timeoutMsg, 0)
			_ = storage.UpdateSubTaskStatus(taskID, "timed_out", "", timeoutMsg)
			a.publishSubagentStatus(parentSessionID, taskID, "timed_out")
			a.maybeNotifyBatch(parentSessionID, userID, parentTaskID)
		} else {
			logger.Warn("subagent", userID, childSessionID,
				"subagent failed: "+err.Error(), 0)
			_ = storage.UpdateSubTaskStatus(taskID, "failed", "", err.Error())
			a.publishSubagentStatus(parentSessionID, taskID, "failed")
			a.maybeNotifyBatch(parentSessionID, userID, parentTaskID)
		}
		return
	}

	finalResult := sw.result()
	logger.Info("subagent", userID, childSessionID,
		"subagent succeeded", 0)
	_ = storage.UpdateSubTaskStatus(taskID, "succeeded", finalResult, "")
	a.maybeNotifyBatch(parentSessionID, userID, parentTaskID)
}

// notifyParent 子 agent 完成后触发父会话的新一轮 agent 执行，将结果或错误注入父 session。
// 根据父会话来源路由到对应的回调通道：
//   - feishu 会话 → feishu.RunForSession（通过飞书 API 发送卡片）
//   - web 会话    → push.CronWriter + a.Run（通过 /api/notify SSE 推送到父前端）
//
// label 为任务标签（可为空），有值时用于美化通知消息标题。
func (a *Agent) notifyParent(parentSessionID, userID string, taskID uint, label, finalResult, errMsg string) {
	// setNotify 辅助：写入 notify_status，失败只记日志（不影响主流程）
	setNotify := func(status, notifyErr string) {
		if dbErr := storage.UpdateSubTaskNotifyStatus(taskID, status, notifyErr); dbErr != nil {
			logger.Warn("subagent", userID, parentSessionID,
				fmt.Sprintf("notifyParent: update notify_status failed (task #%d): %v", taskID, dbErr), 0)
		}
	}

	parentSess, err := storage.GetSession(parentSessionID)
	if err != nil || parentSess == nil {
		msg := fmt.Sprintf("notifyParent: get parent session failed (task #%d)", taskID)
		logger.Warn("subagent", userID, parentSessionID, msg, 0)
		setNotify("parent_missing", "")
		return
	}

	// 构造通知标题：有 label 时显示为 "#7「搜索竞品」"，无 label 时显示为 "#7"
	taskTitle := fmt.Sprintf("#%d", taskID)
	if label != "" {
		taskTitle = fmt.Sprintf("#%d「%s」", taskID, label)
	}

	var notifyMsg string
	if errMsg != "" {
		notifyMsg = fmt.Sprintf("[子任务 %s 失败]\n%s", taskTitle, errMsg)
	} else {
		notifyMsg = fmt.Sprintf("[子任务 %s 已完成]\n结果如下：\n\n%s", taskTitle, finalResult)
	}

	if parentSess.Source == "feishu" && parentSess.FeishuPeer != "" {
		cfg, cfgErr := storage.GetFeishuConfig(parentSess.UserID)
		if cfgErr != nil || cfg == nil || cfg.AppID == "" {
			logger.Warn("subagent", userID, parentSessionID,
				fmt.Sprintf("notifyParent: no feishu config (task #%d)", taskID), 0)
			setNotify("failed", "feishu config missing")
			return
		}
		receiveIDType := receiveIDTypeFromPeer(parentSess.FeishuPeer)
		// RunForSession 无返回值（异步执行），标记 session_queued 表示已提交给飞书 pipeline
		feishu.RunForSession(a.bgCtx, userID, parentSessionID, parentSess.FeishuPeer, receiveIDType, cfg.AppID, notifyMsg)
		setNotify("session_queued", "")
		return
	}

	// 在父 session 上触发新一轮 LLM 执行（followup），让父 agent 处理子任务结果。
	// cron 来源：父会话无前端 SSE 订阅者，用 resultWriter 静默回写，避免向空通道推送事件；
	// 但 a.Run 仍会写入 session_messages，后续链式子 agent 可读到完整对话历史。
	// web 来源：用 CronWriter 同时推送 SSE 事件到前端 /api/notify 通道，让用户实时可见。

	// 序列化对同一父会话的并发 followup LLM 轮次：
	// 多个子任务几乎同时完成时，各 goroutine 会竞争 a.Run(parentSessionID,...)，
	// 不加锁会导致各轮 LLM 读到相同的历史快照并互相覆盖写入结果。
	// per-session 粒度：不同父会话互不阻塞。
	notifyLockVal, _ := a.notifyMu.LoadOrStore(parentSessionID, &sync.Mutex{})
	notifyLock := notifyLockVal.(*sync.Mutex)
	notifyLock.Lock()
	defer notifyLock.Unlock()

	ctx, cancel := context.WithTimeout(a.bgCtx, subagentTimeout)
	defer cancel()
	ctx = withInternalRun(ctx) // 系统注入消息，不写入 origin_session_messages（不显示为用户气泡）
	setNotify("session_queued", "")
	var w StreamWriter
	if parentSess.Source == "cron" {
		w = &resultWriter{}
	} else {
		w = push.NewCronWriterSilent(parentSessionID) // 不发 cron_start，避免显示为用户气泡
	}
	if runErr := a.Run(ctx, userID, parentSessionID, notifyMsg, w); runErr != nil {
		logger.Warn("subagent", userID, parentSessionID,
			fmt.Sprintf("notifyParent: parent run failed (task #%d): %s", taskID, runErr.Error()), 0)
		setNotify("failed", runErr.Error())
		return
	}
	setNotify("delivered", "")
}

// notifyStateChange 向父会话推送轻量状态变更通知（不触发 LLM 调用）。
// 仅在 notify_policy=state_changes 时于 running 阶段调用。
func (a *Agent) notifyStateChange(parentSessionID, userID string, taskID uint, label, newStatus string) {
	sess, err := storage.GetSession(parentSessionID)
	if err != nil || sess == nil {
		return
	}
	// "running" 状态已由各端的初始信号覆盖：
	//   飞书 → FeishuSubagentWriter 构造时发送初始进度卡片
	//   web  → SubagentWriter 发送 subagent_start 事件
	// 无需再发独立通知，避免冗余消息。若未来新增其他中间状态，在此处补充。
	if newStatus == "running" {
		return
	}
	switch sess.Source {
	case "web":
		b, _ := json.Marshal(map[string]any{
			"type":    "subagent_state",
			"task_id": taskID,
			"label":   label,
			"status":  newStatus,
		})
		push.Default.Publish(parentSessionID, b)
	}
}

// notifyProgressUpdate 向 web 父会话推送子任务进度更新（不触发 LLM 调用）。
// 飞书来源由 RunBackground 的进度闭包直接调用 feishuSubagentAdapter.UpdateMilestone，
// 更新同一张进度卡片，不在此函数中处理，避免发送重复的独立文本消息。
func (a *Agent) notifyProgressUpdate(parentSessionID, userID string, taskID uint, label, progress string) {
	sess, err := storage.GetSession(parentSessionID)
	if err != nil || sess == nil {
		return
	}
	if sess.Source != "web" {
		return
	}
	// 使用独立事件类型 subagent_report_progress，与
	// SubagentWriter.WriteProgress 发出的工具步骤事件（subagent_progress）区分：
	//   subagent_progress        → {step, detail, elapsed_ms}  工具调用步骤
	//   subagent_report_progress → {label, progress}           子 agent 主动上报的进度摘要
	b, _ := json.Marshal(map[string]any{
		"type":     "subagent_report_progress",
		"task_id":  taskID,
		"label":    label,
		"progress": progress,
	})
	push.Default.Publish(parentSessionID, b)
}

// notifyMidTask 子 agent 调用 notify_parent 工具时触发：向父 session 注入中间消息并
// 启动新一轮 LLM 执行，使父 agent 能实时感知子任务阶段性更新。
// 路由逻辑与 notifyParent 相同（feishu / cron / web），但消息前缀为"中间更新"。
// 此方法在独立 goroutine 中调用（由 WithParentNotifier 闭包启动），不阻塞子任务。
func (a *Agent) notifyMidTask(parentSessionID, userID string, taskID uint, label, message string) {
	parentSess, err := storage.GetSession(parentSessionID)
	if err != nil || parentSess == nil {
		logger.Warn("subagent", userID, parentSessionID,
			fmt.Sprintf("notifyMidTask: get parent session failed (task #%d)", taskID), 0)
		return
	}

	taskTitle := fmt.Sprintf("#%d", taskID)
	if label != "" {
		taskTitle = fmt.Sprintf("#%d「%s」", taskID, label)
	}
	notifyMsg := fmt.Sprintf("[子任务 %s 中间更新]\n%s", taskTitle, message)

	if parentSess.Source == "feishu" && parentSess.FeishuPeer != "" {
		cfg, cfgErr := storage.GetFeishuConfig(parentSess.UserID)
		if cfgErr != nil || cfg == nil || cfg.AppID == "" {
			logger.Warn("subagent", userID, parentSessionID,
				fmt.Sprintf("notifyMidTask: no feishu config (task #%d)", taskID), 0)
			return
		}
		receiveIDType := receiveIDTypeFromPeer(parentSess.FeishuPeer)
		feishu.RunForSession(a.bgCtx, userID, parentSessionID, parentSess.FeishuPeer, receiveIDType, cfg.AppID, notifyMsg)
		return
	}

	// 与 notifyParent 相同的序列化策略：复用同一把 per-session 锁，
	// 确保中间更新与完成通知也不会并发写同一父会话。
	notifyLockVal, _ := a.notifyMu.LoadOrStore(parentSessionID, &sync.Mutex{})
	notifyLock := notifyLockVal.(*sync.Mutex)
	notifyLock.Lock()
	defer notifyLock.Unlock()

	ctx, cancel := context.WithTimeout(a.bgCtx, 10*time.Minute)
	defer cancel()
	ctx = withInternalRun(ctx) // 系统注入消息，不写入 origin_session_messages（不显示为用户气泡）
	var w StreamWriter
	if parentSess.Source == "cron" {
		w = &resultWriter{}
	} else {
		w = push.NewCronWriterSilent(parentSessionID) // 不发 cron_start，避免显示为用户气泡
	}
	if runErr := a.Run(ctx, userID, parentSessionID, notifyMsg, w); runErr != nil {
		logger.Warn("subagent", userID, parentSessionID,
			fmt.Sprintf("notifyMidTask: parent run failed (task #%d): %s", taskID, runErr.Error()), 0)
	}
}

// maybeNotifyBatch 检查本批次（未通知的同级子任务）是否全部进入终态，若是则触发一次父会话批量通知。
//
// 与旧实现的差异：
//   - 使用 UnnotifiedSiblingsDone 代替 AllSiblingsDone，只考虑 notify_status='' 的任务，
//     已通知过的旧批次任务不计入；这样父 agent 在同一会话内多次 spawn_subagent（如重试某个子任务）
//     时，每批新任务完成后都能独立触发通知，不会被旧批次的标记阻断。
//   - 去重键从 parentSessionID 改为 "parentSessionID:maxTaskID"，确保不同批次互不干扰，
//     同一批次内并发完成时仍只触发一次。
func (a *Agent) maybeNotifyBatch(parentSessionID, userID string, parentTaskID uint) {
	lockVal, _ := a.batchCheckMu.LoadOrStore(parentSessionID, &sync.Mutex{})
	lock := lockVal.(*sync.Mutex)
	lock.Lock()

	allDone, tasks, err := storage.UnnotifiedSiblingsDone(parentSessionID, parentTaskID)
	if err != nil {
		logger.Warn("subagent", userID, parentSessionID,
			fmt.Sprintf("maybeNotifyBatch: check siblings failed: %v", err), 0)
		lock.Unlock()
		return
	}
	if !allDone || len(tasks) == 0 {
		lock.Unlock()
		return
	}

	// 以本批次最大 task ID 作为去重键：同批并发完成时只有一个 goroutine 触发通知，
	// 同时不影响后续批次（不同 maxID → 不同键）。
	maxID := uint(0)
	for _, t := range tasks {
		if t.ID > maxID {
			maxID = t.ID
		}
	}
	batchKey := fmt.Sprintf("%s:%d", parentSessionID, maxID)
	if _, already := a.batchNotifiedSet.Load(batchKey); already {
		lock.Unlock()
		return
	}
	a.batchNotifiedSet.Store(batchKey, struct{}{})
	lock.Unlock()

	a.notifyBatch(parentSessionID, userID, tasks)
}

// notifyBatch 将所有同级子任务的结果汇总为一条消息，触发父会话的新一轮 LLM 执行。
// 路由逻辑与 notifyParent 相同（feishu / cron / web）。
func (a *Agent) notifyBatch(parentSessionID, userID string, tasks []storage.SubTask) {
	if len(tasks) == 0 {
		return
	}

	// setAllNotify 批量更新本批次所有任务的 notify_status
	setAllNotify := func(status, notifyErr string) {
		for _, t := range tasks {
			if dbErr := storage.UpdateSubTaskNotifyStatus(t.ID, status, notifyErr); dbErr != nil {
				logger.Warn("subagent", userID, parentSessionID,
					fmt.Sprintf("notifyBatch: update notify_status failed (task #%d): %v", t.ID, dbErr), 0)
			}
		}
	}

	// 若所有任务均为 silent，无需触发通知
	allSilent := true
	for _, t := range tasks {
		if t.NotifyPolicy != "silent" {
			allSilent = false
			break
		}
	}
	if allSilent {
		return
	}

	// 构造聚合通知消息
	var sb strings.Builder
	sb.WriteString("[所有子任务已完成]\n各任务结果如下：\n\n")
	for _, t := range tasks {
		title := fmt.Sprintf("#%d", t.ID)
		if t.Label != "" {
			title = fmt.Sprintf("#%d「%s」", t.ID, t.Label)
		}
		if t.ErrorMsg != "" {
			fmt.Fprintf(&sb, "▶ 子任务 %s 失败：%s\n\n", title, t.ErrorMsg)
		} else {
			fmt.Fprintf(&sb, "▶ 子任务 %s 已完成：\n%s\n\n", title, t.Result)
		}
	}
	notifyMsg := strings.TrimRight(sb.String(), "\n")

	parentSess, err := storage.GetSession(parentSessionID)
	if err != nil || parentSess == nil {
		logger.Warn("subagent", userID, parentSessionID,
			"notifyBatch: get parent session failed", 0)
		setAllNotify("failed", "parent session missing")
		return
	}

	if parentSess.Source == "feishu" && parentSess.FeishuPeer != "" {
		cfg, cfgErr := storage.GetFeishuConfig(parentSess.UserID)
		if cfgErr != nil || cfg == nil || cfg.AppID == "" {
			logger.Warn("subagent", userID, parentSessionID,
				"notifyBatch: no feishu config", 0)
			setAllNotify("failed", "feishu config missing")
			return
		}
		receiveIDType := receiveIDTypeFromPeer(parentSess.FeishuPeer)
		feishu.RunForSession(a.bgCtx, userID, parentSessionID, parentSess.FeishuPeer, receiveIDType, cfg.AppID, notifyMsg)
		setAllNotify("session_queued", "")
		return
	}

	// web / cron：序列化 LLM 轮次（与 notifyParent 共用同一把 per-session 锁）
	notifyLockVal, _ := a.notifyMu.LoadOrStore(parentSessionID, &sync.Mutex{})
	notifyLock := notifyLockVal.(*sync.Mutex)
	notifyLock.Lock()
	defer notifyLock.Unlock()

	setAllNotify("session_queued", "")
	ctx, cancel := context.WithTimeout(a.bgCtx, subagentTimeout)
	defer cancel()
	ctx = withInternalRun(ctx)

	var w StreamWriter
	if parentSess.Source == "cron" {
		w = &resultWriter{}
	} else {
		w = push.NewCronWriterSilent(parentSessionID)
	}
	if runErr := a.Run(ctx, userID, parentSessionID, notifyMsg, w); runErr != nil {
		logger.Warn("subagent", userID, parentSessionID,
			fmt.Sprintf("notifyBatch: parent run failed: %s", runErr.Error()), 0)
		setAllNotify("failed", runErr.Error())
		return
	}
	setAllNotify("delivered", "")
}

// subagentWriterIface 内部接口：在标准 StreamWriter 基础上增加 result() 方法，
// 避免向 agent.StreamWriter 公共接口泄露实现细节。
type subagentWriterIface interface {
	StreamWriter
	result() string
}

// newSubagentWriter 根据父会话来源返回适配的 subagentWriterIface：
//   - feishu 会话 → feishuSubagentAdapter（包装 FeishuSubagentWriter）
//   - 其他来源    → SubagentWriter（webcli push 通道）
//
// label 为任务短标签，传给 SubagentWriter 以写入 subagent_start 事件，
// 供前端在折叠卡片标题展示。
// notifyPolicy 传给 FeishuSubagentWriter，控制 sendFinal 的内容截断行为。
func (a *Agent) newSubagentWriter(parentSessionID string, taskID uint, taskDesc, label, notifyPolicy string) subagentWriterIface {
	if parentSess, err := storage.GetSession(parentSessionID); err == nil &&
		parentSess != nil &&
		parentSess.Source == "feishu" &&
		parentSess.FeishuPeer != "" {

		if cfg, err := storage.GetFeishuConfig(parentSess.UserID); err == nil &&
			cfg != nil && cfg.AppID != "" {

			// 飞书卡片展示标题：优先用 label，否则从 prompt 中提取实际任务文本，
			// 避免显示 "[Subagent Task]\n你是一个专注执行单一任务的..." 等系统提示前缀。
			displayTitle := label
			if displayTitle == "" {
				displayTitle = extractTaskTitle(taskDesc)
			}

			receiveIDType := receiveIDTypeFromPeer(parentSess.FeishuPeer)
			fw := feishu.NewSubagentWriter(
				parentSess.UserID,
				parentSess.FeishuPeer,
				cfg.AppID,
				receiveIDType,
				displayTitle,
				notifyPolicy,
			)
			return &feishuSubagentAdapter{w: fw}
		}
	}

	// 默认：webcli SSE 通道，传入 label 供前端折叠卡片标题展示
	return NewSubagentWriter(parentSessionID, taskID, taskDesc, label)
}

// extractTaskTitle 从子 agent prompt 中提取实际任务描述。
// prompt 格式：[Subagent Task]\n...\n父 Agent 分配给你的工作是：\n\n{task}\n\n...
// 若格式不匹配（如直接派发的自定义 prompt），原样返回 taskDesc。
func extractTaskTitle(taskDesc string) string {
	const marker = "父 Agent 分配给你的工作是：\n\n"
	idx := strings.Index(taskDesc, marker)
	if idx < 0 {
		return taskDesc
	}
	rest := taskDesc[idx+len(marker):]
	// 截取到下一个 \n\n（背景信息段或结束语之前）
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// receiveIDTypeFromPeer 根据飞书 peer ID 前缀推断 receive_id_type。
// 群聊 chat_id 以 "oc_" 开头，单聊 open_id 以 "ou_" 开头。
func receiveIDTypeFromPeer(peer string) string {
	if strings.HasPrefix(peer, "oc_") {
		return "chat_id"
	}
	return "open_id"
}

// publishSubagentStatus 向 web 父会话推送子任务最终状态事件，供前端将子任务卡片更新为准确的终态标签。
// 飞书等非 SSE 来源无订阅者，push 消息被静默丢弃，无副作用。
// status 取值：cancelled | timed_out | killed | failed（succeeded 由 subagent_end 事件覆盖，无需重复推送）。
func (a *Agent) publishSubagentStatus(parentSessionID string, taskID uint, status string) {
	b, _ := json.Marshal(map[string]any{
		"type":    "subagent_status",
		"task_id": taskID,
		"status":  status,
	})
	push.Default.Publish(parentSessionID, b)
}

// feishuSubagentAdapter 包装 FeishuSubagentWriter，使其满足 subagentWriterIface。
// FeishuSubagentWriter 自身收集了 LLM 文字输出，通过 textBuf 暴露给 result()。
type feishuSubagentAdapter struct {
	w *feishu.FeishuSubagentWriter
}

func (a *feishuSubagentAdapter) WriteText(text string) error {
	return a.w.WriteText(text)
}
func (a *feishuSubagentAdapter) WriteProgress(step, detail, callID string, ms int64) error {
	return a.w.WriteProgress(step, detail, callID, ms)
}
func (a *feishuSubagentAdapter) WriteInteractive(kind string, data any) error {
	return a.w.WriteInteractive(kind, data)
}
func (a *feishuSubagentAdapter) WriteSpeaker(name string) error {
	return a.w.WriteSpeaker(name)
}
func (a *feishuSubagentAdapter) WriteImage(url string) error {
	return a.w.WriteImage(url)
}
func (a *feishuSubagentAdapter) WriteEnd() error {
	return a.w.WriteEnd()
}
func (a *feishuSubagentAdapter) WriteError(msg string) error {
	return a.w.WriteError(msg)
}
func (a *feishuSubagentAdapter) result() string {
	return a.w.TextResult()
}
