// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/handler/protocol.go — WS / SSE 统一推送消息格式
// WS 以 JSON 帧推送，SSE 以 "data: {...}\n\n" 推送，JSON 结构完全一致，前端可统一解析。
package handler

import "encoding/json"

// OutMsg 服务端推送给客户端的统一消息结构
//
// type 字段决定有效载荷：
//
//	"text"        — LLM 输出的文字片段，content 有值
//	"image"       — 工具产出的图片，content 为 Web 路径（如 /output/3/abc.png），前端内联展示
//	"progress"    — 执行进度事件，step / detail / elapsed_ms 有值
//	"interactive" — 需要用户交互，step 为交互类型（options/confirm），data 为结构化载荷
//	"speaker"     — 当前活跃技能的优雅名称，content 为名称字符串（前端可展示为 AI 发言者名字）
//	"end"         — 本轮回答结束，无附加字段
//	"error"       — 出错，content 为错误描述
type OutMsg struct {
	// Type 消息类型：text | progress | interactive | end | error
	Type string `json:"type"`

	// Content 文本内容（type=text / error 时使用）
	Content string `json:"content,omitempty"`

	// Step 步骤/子类型标识，机器可读
	// progress 时取值：tool_call | tool_done | tool_error | compress_start | compress_done | compress_error | <PROGRESS_LABEL>
	// interactive 时取值：options | confirm
	Step string `json:"step,omitempty"`

	// Detail 进度描述（type=progress 时使用），人类可读
	Detail string `json:"detail,omitempty"`

	// ElapsedMs 自本轮 Agent 启动至此事件的耗时（毫秒）
	ElapsedMs int64 `json:"elapsed_ms,omitempty"`

	// Data 结构化载荷（type=interactive 时使用），前端按 step 解析
	Data json.RawMessage `json:"data,omitempty"`
}
