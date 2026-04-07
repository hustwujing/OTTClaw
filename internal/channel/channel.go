// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/channel/channel.go — 通道接入统一框架：接口定义
//
// 新增一条渠道只需：
//  1. 实现 Adapter 接口（3 个方法）
//  2. 提供渠道专属 writer（嵌入 BaseWriter）
//  3. 凭证存储 + tool handler + main.go 两行
package channel

import "context"

// StreamWriter 消除 feishu/wecom 各自定义的副本
type StreamWriter interface {
	WriteText(text string) error
	WriteProgress(step, detail, callID string, elapsedMs int64) error
	WriteInteractive(kind string, data any) error
	WriteSpeaker(name string) error
	WriteImage(url string) error
	WriteEnd() error
	WriteError(msg string) error
}

// AgentRunFunc agent 执行函数类型，消除各渠道包的副本
type AgentRunFunc func(ctx context.Context, userID, sessionID, userText string, writer StreamWriter) error

// WriterFactory 由 adapter 提供，在 session 确定后构造渠道专属 writer
type WriterFactory func(sessionID string) StreamWriter

// DispatchFunc 由框架提供；adapter 收到消息时调用。
// ctx 已由 adapter 富化（如飞书注入 appID），peerID 为对话方 ID，userText 为已解析文本。
type DispatchFunc func(ctx context.Context, peerID, userText string, wf WriterFactory)

// Adapter 新渠道只需实现此接口（3 个方法）
type Adapter interface {
	// Name 返回渠道名称（如 "feishu" / "wecom"），用于日志和 session source 字段
	Name() string
	// GetConfiguredUserIDs 返回已配置凭证的用户 ID 列表，供 StartAll 迭代
	GetConfiguredUserIDs() ([]string, error)
	// Connect 建立连接并阻塞直到 ctx 取消或发生错误；
	// 收到消息时通过 dispatch 分发，由框架负责重连
	Connect(ctx context.Context, ownerUserID string, dispatch DispatchFunc) error
}

// ── ExecAutoApprove 上下文标记 ───────────────────────────────────────────────
//
// 微信、飞书等无法弹出交互式确认框的渠道，在调用 dispatch 前注入此标记。
// exec 工具读取该标记后直接执行命令，无需等待用户点击确认。

type execAutoApproveKey struct{}

// WithExecAutoApprove 向 ctx 注入"exec 自动审批"标记。
func WithExecAutoApprove(ctx context.Context) context.Context {
	return context.WithValue(ctx, execAutoApproveKey{}, true)
}

// ExecAutoApproveFromCtx 读取"exec 自动审批"标记。
func ExecAutoApproveFromCtx(ctx context.Context) bool {
	v, _ := ctx.Value(execAutoApproveKey{}).(bool)
	return v
}
