// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/subagent_policy.go — 子 agent 工具访问策略
//
// 子 agent 在后台无人值守地运行，部分工具对它毫无意义（或存在安全风险），
// 应在向 LLM 提供工具定义时过滤掉，防止 LLM 误调用。
//
// 策略参考 openclaw 的 SUBAGENT_TOOL_DENY_ALWAYS 设计：
//   - 在 agent.initRun 中，当 isSubagent=true 时，通过 FilterSubagentTools
//     从 ToolDefinitions() 结果里移除 deny 列表内的工具。
//   - 当 isSubagent=false 时，通过 FilterParentTools 移除仅子 agent 可用的工具。
//   - LLM 不会收到这些工具的定义，因此不会尝试调用它们。
package tool

import "OTTClaw/internal/llm"

// subagentDeniedTools 子 agent 的工具黑名单。
// 这些工具在后台子 agent 中无意义或有风险，应对 LLM 隐藏：
//
//   - cron           : 调度任务是主 agent 的职责，子 agent 不应自行创建 cron job
//   - update_role_md : 修改系统角色文件风险高，只能由主 agent 操作
//   - feishu         : 直接向飞书用户发消息，会绕过父 agent 的渠道协调
//   - wecom          : 同上，企业微信消息渠道协调由父 agent 负责
//   - exec_run       : 父 agent 两步审批流的第二步，子 agent exec 直接执行不走 pending 流程
var subagentDeniedTools = map[string]struct{}{
	"cron":           {},
	"update_role_md": {},
	"feishu":         {},
	"wecom":          {},
	"exec_run":       {},
}

// parentDeniedTools 父 agent 的工具黑名单（仅子 agent 可用）。
// 这些工具依赖子 agent 运行时注入的 context（taskID / parentNotifier），
// 父 agent 调用会直接报错，对 LLM 暴露定义只会浪费 token：
//
//   - report_task_progress : 向父会话上报子任务进度，父 agent 本身无父任务
//   - notify_parent        : 唤醒父 agent LLM turn，父 agent 没有父会话
var parentDeniedTools = map[string]struct{}{
	"report_task_progress": {},
	"notify_parent":        {},
}

// FilterSubagentTools 过滤工具列表，移除子 agent 禁用的工具。
// 在 agent.initRun 中，当 isSubagent=true 时调用。
func FilterSubagentTools(tools []llm.Tool) []llm.Tool {
	filtered := make([]llm.Tool, 0, len(tools))
	for _, t := range tools {
		if _, denied := subagentDeniedTools[t.Function.Name]; !denied {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// FilterParentTools 过滤工具列表，移除仅子 agent 可用的工具。
// 在 agent.initRun 中，当 isSubagent=false 时调用。
func FilterParentTools(tools []llm.Tool) []llm.Tool {
	filtered := make([]llm.Tool, 0, len(tools))
	for _, t := range tools {
		if _, denied := parentDeniedTools[t.Function.Name]; !denied {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
