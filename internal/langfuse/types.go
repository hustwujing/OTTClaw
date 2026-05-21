// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/langfuse/types.go — Langfuse API 数据类型定义
package langfuse

import "time"

// Event 是 Langfuse /api/public/ingestion 批量接口的单条事件。
type Event struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Body      any       `json:"body"`
}

// TraceBody 对应 trace-create / trace-update 事件的 body。
type TraceBody struct {
	ID        string         `json:"id"`
	Name      string         `json:"name,omitempty"`
	UserID    string         `json:"userId,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
	Input     any            `json:"input,omitempty"`
	Output    any            `json:"output,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
}

// GenerationBody 对应 generation-create / generation-update 事件的 body。
type GenerationBody struct {
	ID                  string     `json:"id"`
	TraceID             string     `json:"traceId"`
	ParentObservationID string     `json:"parentObservationId,omitempty"`
	Name                string     `json:"name,omitempty"`
	Model               string     `json:"model,omitempty"`
	StartTime           time.Time  `json:"startTime"`
	EndTime             *time.Time `json:"endTime,omitempty"`
	Input               any        `json:"input,omitempty"`
	Output              any        `json:"output,omitempty"`
	Usage               *UsageBody `json:"usage,omitempty"`
}

// SpanBody 对应 span-create / span-update 事件的 body。
type SpanBody struct {
	ID                  string     `json:"id"`
	TraceID             string     `json:"traceId"`
	ParentObservationID string     `json:"parentObservationId,omitempty"`
	Name                string     `json:"name,omitempty"`
	StartTime           time.Time  `json:"startTime"`
	EndTime             *time.Time `json:"endTime,omitempty"`
	Input               any        `json:"input,omitempty"`
	Output              any        `json:"output,omitempty"`
}

// UsageBody 记录 LLM 调用的 token 用量。
type UsageBody struct {
	Input  int `json:"input,omitempty"`
	Output int `json:"output,omitempty"`
	Total  int `json:"total,omitempty"`
}

// ScoreBody 对应 score-create 事件的 body，用于 Task Unit 评估上报。
// id 和 traceId 均为必填项（Langfuse ingestion batch API 要求）。
// Value 对 NUMERIC/BOOLEAN 传 float64，对 CATEGORICAL 传 string。
type ScoreBody struct {
	ID       string `json:"id"`
	TraceID  string `json:"traceId"`
	Name     string `json:"name"`
	Value    any    `json:"value"`
	Comment  string `json:"comment,omitempty"`
	DataType string `json:"dataType,omitempty"` // "NUMERIC" | "BOOLEAN" | "CATEGORICAL"
}
