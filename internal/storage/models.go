// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/storage/models.go — SQLite 数据库模型定义（invite_codes + sessions + session_messages）
package storage

import "time"

// CronJob 定时任务表
type CronJob struct {
	ID             string     `gorm:"primaryKey;column:id"`
	UserID         string     `gorm:"column:user_id;index;not null"`
	Name           string     `gorm:"column:name;not null"`
	Enabled        bool       `gorm:"column:enabled;default:true"`
	ScheduleJSON   string     `gorm:"column:schedule_json;type:text"`        // {"kind":"cron","expr":"0 9 * * *"} 等
	Message          string     `gorm:"column:message;type:text"`              // 触发时发给 agent 的消息
	CreatorSessionID string     `gorm:"column:creator_session_id;default:''"` // 创建任务时所在的 web session，触发时回写结果
	DeleteAfterRun   bool       `gorm:"column:delete_after_run;default:false"` // at 类型自动 true
	LastRunAt      *time.Time `gorm:"column:last_run_at"`
	NextRunAt      *time.Time `gorm:"column:next_run_at;index"` // 预计算，加速调度查询
	RunCount       int        `gorm:"column:run_count;default:0"`
	CreatedAt      time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;autoUpdateTime"`
}

func (CronJob) TableName() string { return "cron_jobs" }

// InviteCode 邀请码表：每条记录对应一个账号名 + 邀请码对
type InviteCode struct {
	Code      string     `gorm:"primaryKey;column:code"`
	UserID    string     `gorm:"column:user_id;not null;index"`
	MaxUses   int        `gorm:"column:max_uses;default:0"`   // 0 = 不限设备数
	UseCount  int        `gorm:"column:use_count;default:0"`  // 已登录设备数
	CreatedAt time.Time  `gorm:"column:created_at;autoCreateTime"`
	ExpiresAt *time.Time `gorm:"column:expires_at"` // nil = 永不过期
}

func (InviteCode) TableName() string { return "invite_codes" }

// Session 会话表，存储会话元数据和 KV 上下文
type Session struct {
	SessionID        string    `gorm:"primaryKey;column:session_id"`
	UserID           string    `gorm:"column:user_id;index;not null"`
	KVData           string    `gorm:"column:kv_data;type:text"`              // JSON 格式的 KV 上下文
	Title            string    `gorm:"column:title;default:''"`               // AI 生成的会话标题（空则前端用首条消息做预览）
	Source           string    `gorm:"column:source;default:'web'"`           // 来源：web | feishu | subagent
	FeishuPeer       string    `gorm:"column:feishu_peer;default:''"`         // 飞书对话方 ID（open_id 或 chat_id）
	ParentSessionID  string    `gorm:"column:parent_session_id;default:''"` // 血缘父会话（显式续话或压缩衍生时设置）
	IsSubagent       bool      `gorm:"column:is_subagent;default:false"`      // 是否为子 agent 会话
	SubagentTask     string    `gorm:"column:subagent_task;type:text;default:''"` // 子 agent 被分配的任务描述
	CreatedAt        time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt        time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// SubTask 子 agent 任务表：追踪每个子 agent 任务的完整生命周期
type SubTask struct {
	ID              uint      `gorm:"primaryKey;autoIncrement;column:id"`
	UserID          string    `gorm:"column:user_id;index;not null"`
	ParentSessionID string    `gorm:"column:parent_session_id;index;not null"` // 发起方主会话
	ChildSessionID  string    `gorm:"column:child_session_id;uniqueIndex;not null"` // 子 agent 会话
	Runtime         string    `gorm:"column:runtime;not null;default:'subagent';index"` // 来源 runtime：subagent | cron | feishu
	Label           string    `gorm:"column:label;default:''"` // 简短可读任务标签（由父 agent 在 spawn_subagent 时指定）
	ParentTaskID    uint      `gorm:"column:parent_task_id;default:0;index"` // 父任务 ID（0 表示顶层任务，>0 表示嵌套子任务）
	TaskDesc        string    `gorm:"column:task_desc;type:text"`              // 任务描述（传给子 agent 的完整 prompt）
	Status          string    `gorm:"column:status;not null;default:'queued';index"` // queued | running | succeeded | failed | timed_out | lost | cancelled | killed
	Result          string     `gorm:"column:result;type:text"`                 // 子 agent 最终输出（succeeded 时有值）
	ErrorMsg        string     `gorm:"column:error_msg;type:text"`              // 失败原因（failed 时有值）
	ProgressSummary string     `gorm:"column:progress_summary;type:text;default:''"` // 运行中进度摘要，由子 agent 主动更新
	StartedAt       *time.Time `gorm:"column:started_at"`                       // status→running 时写入
	EndedAt         *time.Time `gorm:"column:ended_at"`                         // 进入终态时写入
	CleanupAfter    *time.Time `gorm:"column:cleanup_after;index"`               // 到期后允许 GC 删除；nil = 使用全局保留窗口
	NotifyPolicy    string     `gorm:"column:notify_policy;default:'done_only'"` // 通知策略：done_only | state_changes | silent
	NotifyStatus    string     `gorm:"column:notify_status;default:''"`          // 父会话通知投递状态：'' | session_queued | delivered | failed
	NotifyError     string     `gorm:"column:notify_error;type:text;default:''"` // 投递失败原因
	CreatedAt       time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;autoUpdateTime"`
}

func (SubTask) TableName() string { return "sub_tasks" }

// TableName 指定表名
func (Session) TableName() string { return "sessions" }

// FeishuConfig 飞书机器人配置表，每个用户独立一份
type FeishuConfig struct {
	UserID       string    `gorm:"primaryKey;column:user_id"`
	AppID        string    `gorm:"column:app_id;default:''"`
	AppSecretEnc string    `gorm:"column:app_secret_enc;type:text;default:''"` // AES-GCM 加密后的 AppSecret
	WebhookURL   string    `gorm:"column:webhook_url;type:text;default:''"`   // Webhook 模式 URL（与 Bot 二选一）
	SelfOpenID   string    `gorm:"column:self_open_id;default:''"`            // 用户自己的飞书 open_id（用于 Bot 主动发消息给自己）
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (FeishuConfig) TableName() string { return "feishu_configs" }

// WeComConfig 企业微信机器人配置表，每个用户独立一份
// 企微仅支持群机器人 Webhook，无需 Bot 凭证
type WeComConfig struct {
	UserID     string    `gorm:"primaryKey;column:user_id"`
	WebhookURL string    `gorm:"column:webhook_url;type:text;default:''"` // 群机器人 Webhook URL
	UpdatedAt  time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (WeComConfig) TableName() string { return "wecom_configs" }

// SessionMessage 消息历史表，保存完整对话记录
type SessionMessage struct {
	ID        uint      `gorm:"primaryKey;autoIncrement;column:id"`
	UserID    string    `gorm:"column:user_id;index;not null"`
	SessionID string    `gorm:"column:session_id;index;not null"`
	Role      string    `gorm:"column:role;not null"` // user / assistant / tool / system
	Content   string    `gorm:"column:content;type:text;not null"`
	ToolCallID    string `gorm:"column:tool_call_id"`    // tool 角色时对应的 tool_call_id
	Name          string `gorm:"column:name"`            // tool 角色时的函数名
	ToolCallsJSON string `gorm:"column:tool_calls_json"` // assistant 角色时的 tool_calls JSON（用于跨轮次重建）
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

// TableName 指定表名
func (SessionMessage) TableName() string { return "session_messages" }

// ToolRequest 工具需求表：记录 LLM 在对话中想用但尚未实现的工具
type ToolRequest struct {
	ID           uint      `gorm:"primaryKey;autoIncrement;column:id"`
	Name         string    `gorm:"column:name;not null;index"`        // 工具名，如 send_email
	Description  string    `gorm:"column:description;type:text"`      // 功能简述
	Trigger      string    `gorm:"column:trigger;type:text"`           // 触发场景
	InputSchema  string    `gorm:"column:input_schema;type:text"`      // 输入参数描述
	OutputSchema string    `gorm:"column:output_schema;type:text"`     // 期望输出描述
	UserID       string    `gorm:"column:user_id;index"`
	SessionID    string    `gorm:"column:session_id;index"`
	Status       string    `gorm:"column:status;not null;default:pending;index"` // pending | done
	CloseReason  string    `gorm:"column:close_reason;type:text"`                // 关闭原因（LLM 判断已被某工具覆盖时填写）
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (ToolRequest) TableName() string { return "tool_requests" }

// UserProfile 用户人设表：存储 LLM 撰写的个性化沟通偏好及 Agent 笔记
type UserProfile struct {
	UserID    string    `gorm:"primaryKey;column:user_id"`
	Persona   string    `gorm:"column:persona;type:text;default:''"` // LLM 写入的自由文本人设，空=未初始化
	Notes     string    `gorm:"column:notes;type:text;default:''"`   // §分隔的 Agent 笔记（跨会话持久）
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (UserProfile) TableName() string { return "user_profiles" }

// UserData 用户维度 KV 数据表：跨会话持久存储的用户级别业务数据（有别于 session 维度的临时 KV 和 user_profiles 中的 persona/notes）
type UserData struct {
	ID        uint      `gorm:"primaryKey;autoIncrement;column:id"`
	UserID    string    `gorm:"column:user_id;not null;uniqueIndex:uidx_user_key"`
	Key       string    `gorm:"column:key;not null;uniqueIndex:uidx_user_key"`
	Value     string    `gorm:"column:value;type:text;not null;default:''"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (UserData) TableName() string { return "user_data" }

// CronRunHistory 定时任务执行历史表：每次 cron job 触发记录一条。
// 独立于 sub_tasks，不共享孤儿恢复/状态机/通知语义。
type CronRunHistory struct {
	ID        uint       `gorm:"primaryKey;autoIncrement;column:id"`
	JobID     string     `gorm:"column:job_id;index;not null"`          // 关联 cron_jobs.id
	UserID    string     `gorm:"column:user_id;index;not null"`
	JobName   string     `gorm:"column:job_name;not null"`              // 冗余存储，job 删除后仍可查
	SessionID string     `gorm:"column:session_id;not null"`            // 本次执行使用的 session
	Status    string     `gorm:"column:status;not null;index"`          // running | succeeded | failed | timed_out
	StartedAt time.Time  `gorm:"column:started_at;not null"`
	EndedAt   *time.Time `gorm:"column:ended_at"`
	ErrorMsg  string     `gorm:"column:error_msg;type:text;default:''"`
	CreatedAt time.Time  `gorm:"column:created_at;autoCreateTime"`
}

func (CronRunHistory) TableName() string { return "cron_run_history" }

// TokenUsage LLM 调用 token 消耗记录，每次 LLM 调用写一条
type TokenUsage struct {
	ID               uint      `gorm:"primaryKey;autoIncrement;column:id"`
	UserID           string    `gorm:"column:user_id;index;not null"`
	SessionID        string    `gorm:"column:session_id;index"`
	Model            string    `gorm:"column:model"`
	PromptTokens     int       `gorm:"column:prompt_tokens"`
	CompletionTokens int       `gorm:"column:completion_tokens"`
	TotalTokens      int       `gorm:"column:total_tokens"`
	CreatedAt        time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (TokenUsage) TableName() string { return "token_usages" }

// Attachment 附件元数据，序列化为 JSON 存入 OriginSessionMessage.Attachments
type Attachment struct {
	Type     string `json:"type"`                // "image" | "file"
	URL      string `json:"url"`                 // Web 可访问路径，如 /uploads/A/abc.png
	Filename string `json:"filename"`            // 展示用文件名
	Size     int64  `json:"size"`                // 字节数
	MimeType string `json:"mime_type,omitempty"` // MIME 类型，可选
}

// OriginSessionMessage 用户可见历史记录表，忠实记录需要展示给用户的消息。
// 与 session_messages 不同：无 LLM 内部字段，content 不截断，支持多附件。
// role 只有 "user" | "assistant"，工具调用过程不记录。
type OriginSessionMessage struct {
	ID          uint      `gorm:"primaryKey;autoIncrement;column:id"`
	UserID      string    `gorm:"column:user_id;index;not null"`
	SessionID   string    `gorm:"column:session_id;index"`      // upload 时可能为空
	Role        string    `gorm:"column:role;not null"`          // "user" | "assistant"
	Content     string    `gorm:"column:content;type:text;default:''"`
	Attachments string    `gorm:"column:attachments;type:text"` // JSON 数组，无附件时为空字符串
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (OriginSessionMessage) TableName() string { return "origin_session_messages" }
