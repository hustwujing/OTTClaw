// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/executor.go — 工具注册与执行
//
// 内置工具（合并后）：
//   - read_image        : 按需读取图片并返回多模态内容（本轮 in-memory，不写 DB）
//   - notify            : UI 通知统一入口（action=progress/options/confirm，合并自 send_progress/send_options/send_confirm）
//                         progress 纯 UI，不写 DB；options/confirm 写 DB，下轮上下文可见
//   - skill             : 技能操作统一入口（action=load/run_script/read_asset/read_reference/write/reload，合并自 get_skill_content/run_script/read_asset/write_skill_file/reload_skills）
//   - kv                : 会话 KV 统一入口（action=get/set/append，合并自 kv_get/kv_set/kv_append）
//   - fs                : 文件系统统一入口（action=list/stat/read/write/delete/move/mkdir，合并自 7 个 fs_* 工具）
//   - tool_request      : 工具需求统一入口（action=request/list/close，合并自 3 个工具）
//   - output_file       : 文件输出统一入口（action=write/download，合并自 write_output_file/serve_file_download）
//                         write 写文件后自动生成下载 token，一次调用返回 path+download_url
//   - feishu            : 飞书操作统一入口（action=send/webhook/get_config/set_config，合并自 4 个工具）
//   - wecom             : 企业微信统一入口（action=send/get_config/set_config，合并自 3 个工具）
//   - user_persona      : 用户人设统一入口（action=get/set，合并自 2 个工具）
//   - mcp               : MCP 外接能力统一入口（action=list/detail/call，通过 internal/mcp/registry.go 懒加载）
//
// 依赖注入策略：
//   - ProgressSender    通过 context.Value 注入，供 send_progress 使用
//   - InteractiveSender 通过 context.Value 注入，供 send_options / send_confirm 使用
//   - sessionID         通过 context.Value 注入，供 kv 工具访问数据库
//   均在 agent.Run() 启动时设置，tool 包不直接依赖 agent/handler 包。
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"OTTClaw/config"
	"OTTClaw/internal/llm"
	"OTTClaw/internal/skill"
	"OTTClaw/internal/storage"
)

// skillIDRegex 约束 skill_id 只允许小写字母、数字、下划线
var skillIDRegex = regexp.MustCompile(`^[a-z0-9_]+$`)

// ========== Context 注入：ProgressSender ==========

// ProgressSender 向前端推送进度消息的函数类型，由 agent.Run() 创建并注入
type ProgressSender func(message string) error

type progressCtxKey struct{}

// WithProgressSender 将 ProgressSender 注入 context
func WithProgressSender(ctx context.Context, s ProgressSender) context.Context {
	return context.WithValue(ctx, progressCtxKey{}, s)
}

func senderFromCtx(ctx context.Context) ProgressSender {
	s, _ := ctx.Value(progressCtxKey{}).(ProgressSender)
	return s
}

// ========== Context 注入：InteractiveSender ==========

// InteractiveSender 向前端推送交互控件的函数类型，由 agent.Run() 创建并注入
// kind: 交互类型（options / confirm）
// data: 结构化载荷，前端按 kind 渲染对应控件
type InteractiveSender func(kind string, data any) error

type interactiveCtxKey struct{}

// WithInteractiveSender 将 InteractiveSender 注入 context
func WithInteractiveSender(ctx context.Context, s InteractiveSender) context.Context {
	return context.WithValue(ctx, interactiveCtxKey{}, s)
}

func interactiveSenderFromCtx(ctx context.Context) InteractiveSender {
	s, _ := ctx.Value(interactiveCtxKey{}).(InteractiveSender)
	return s
}

// ========== Context 注入：RoleUpdater ==========

// RoleUpdater 热更新 ROLE.md 的函数类型，由 agent.Run() 创建并注入。
// 调用时：写入 ROLE.md 文件 + 更新 agent singleton 的 roleMD 缓存。
type RoleUpdater func(newContent string) error

type roleUpdaterKey struct{}

// WithRoleUpdater 将 RoleUpdater 注入 context
func WithRoleUpdater(ctx context.Context, u RoleUpdater) context.Context {
	return context.WithValue(ctx, roleUpdaterKey{}, u)
}

func roleUpdaterFromCtx(ctx context.Context) RoleUpdater {
	u, _ := ctx.Value(roleUpdaterKey{}).(RoleUpdater)
	return u
}

// ========== Context 注入：SpeakerSender ==========

// SpeakerSender 向前端推送当前活跃技能名称（优雅名字）的函数类型，由 agent.Run() 注入。
// 每次 skill(action=load) 成功后调用，通知前端切换显示名称。
type SpeakerSender func(name string) error

type speakerCtxKey struct{}

// WithSpeakerSender 将 SpeakerSender 注入 context
func WithSpeakerSender(ctx context.Context, s SpeakerSender) context.Context {
	return context.WithValue(ctx, speakerCtxKey{}, s)
}

func speakerSenderFromCtx(ctx context.Context) SpeakerSender {
	s, _ := ctx.Value(speakerCtxKey{}).(SpeakerSender)
	return s
}

// ========== Context 注入：sessionID ==========

type sessionCtxKey struct{}

// WithSessionID 将当前 sessionID 注入 context，供 KV 工具访问数据库
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

func sessionIDFromCtx(ctx context.Context) string {
	s, _ := ctx.Value(sessionCtxKey{}).(string)
	return s
}

// ========== Context 注入：userID ==========

type userIDCtxKey struct{}

// WithUserID 将当前 userID 注入 context，供 get_current_user 工具读取
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDCtxKey{}, userID)
}

func userIDFromCtx(ctx context.Context) string {
	s, _ := ctx.Value(userIDCtxKey{}).(string)
	return s
}

// ========== Executor ==========

// Handler 工具处理函数签名
type Handler func(ctx context.Context, argsJSON string) (string, error)

// Executor 管理工具注册与分发执行
type Executor struct {
	handlers map[string]Handler
}

// New 创建并注册所有内置工具
func New() *Executor {
	e := &Executor{handlers: make(map[string]Handler)}
	e.register("notify", handleNotify)                   // 合并自 send_progress / send_options / send_confirm
	e.register("skill", e.handleSkill)                   // 合并自 get_skill_content / run_script / read_asset / write_skill_file / reload_skills
	e.register("kv", handleKv)                          // 合并自 kv_get / kv_set / kv_append
	e.register("update_role_md", handleUpdateRoleMD)
	e.register("get_current_user", handleGetCurrentUser)
	e.register("read_file", handleReadFile)
	e.register("fs", handleFs)                           // 合并自 fs_list / fs_stat / fs_read / fs_write / fs_delete / fs_move / fs_mkdir
	e.register("tool_request", e.handleToolRequest)      // 合并自 request_tool / list_tool_requests / close_tool_request
	e.register("output_file", handleOutputFile) // 合并自 write_output_file / serve_file_download
	e.register("send_file_upload", handleSendFileUpload)
	e.register("exec", handleExec)
	e.register("exec_run", handleExecRun)
	e.register("process", handleProcess)
	e.register("feishu", handleFeishu)                   // 合并自 feishu_send / feishu_webhook / get_feishu_config / set_feishu_config
	e.register("wecom", handleWecom)                     // 合并自 wecom_send / get_wecom_config / set_wecom_config
	e.register("browser", handleBrowser)
	e.register("web_fetch", handleWebFetch)
	e.register("read_pdf", handleReadPDF)
	e.register("code_search", handleCodeSearch)
	e.register("cron", handleCron)
	e.register("user_persona", handleUserPersona)        // 合并自 get_user_persona / set_user_persona
	e.register("nano_banana", handleNanoBanana)
	e.register("get_tool_doc", handleGetToolDoc)
	e.register("read_image", handleReadImage)
	e.register("mcp", handleMCP)
	if config.Cfg.MemoryEnabled {
		e.register("memory", handleMemory)
	}
	if config.Cfg.SessionSearchEnabled {
		e.register("session_search", handleSessionSearch)
	}
	if config.Cfg.HonchoEnabled {
		e.register("honcho_profile", handleHonchoProfile)
		e.register("honcho_search", handleHonchoSearch)
		e.register("honcho_context", handleHonchoContext)
		e.register("honcho_conclude", handleHonchoConclude)
	}
	return e
}

func (e *Executor) register(name string, h Handler) {
	e.handlers[name] = h
}

// Names 返回所有已注册工具的名称列表
func (e *Executor) Names() []string {
	names := make([]string, 0, len(e.handlers))
	for name := range e.handlers {
		names = append(names, name)
	}
	return names
}

// SyncToolRequests 将 tool_requests 表中名称匹配"已实现工具"的 pending 记录标记为 done。
// 已实现工具来源：executor 注册的内置工具 + skills 目录已加载的 skill_id。
func (e *Executor) SyncToolRequests() error {
	names := e.Names()
	for _, h := range skill.Store.GetAllHeads() {
		names = append(names, h.SkillID)
	}
	return storage.MarkImplementedToolRequests(names)
}

// Execute 根据工具名称分发执行
func (e *Executor) Execute(ctx context.Context, name, argsJSON string) (string, error) {
	h, ok := e.handlers[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %q", name)
	}
	return h(ctx, argsJSON)
}

// ToolDefinitions 返回所有工具的 LLM function calling 定义。
// initialized=true 后过滤掉仅在 bootstrap 阶段使用的工具，减少 token 消耗。
func (e *Executor) ToolDefinitions() []llm.Tool {
	all := []llm.Tool{
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "notify",
				Description: "UI notification. action: progress (push message, no wait) / options (show choice buttons, STOP and wait for reply) / confirm (show confirm dialog, STOP and wait for reply).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":  map[string]any{"type": "string", "enum": []string{"progress", "options", "confirm"}},
						"message": map[string]any{"type": "string", "description": "progress: text; confirm: action description"},
						"title":   map[string]any{"type": "string", "description": "options: title shown above the buttons"},
						"options": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"label": map[string]any{"type": "string"},
									"value": map[string]any{"type": "string"},
								},
								"required": []string{"label", "value"},
							},
						},
						"confirm_label": map[string]any{"type": "string", "description": "Confirm button text"},
						"cancel_label":  map[string]any{"type": "string", "description": "Cancel button text"},
					},
					"required": []string{"action"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "skill",
				Description: "Skill operations. action: load (load full content, required before executing any skill) / run_script (execute script/) / read_asset (read assets/) / read_reference (read references/) / write (create SKILL.md or script/assets/references file, then call reload) / reload (activate new skills). Call get_tool_doc(\"skill\") for details.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":         map[string]any{"type": "string", "enum": []string{"load", "run_script", "read_asset", "read_reference", "write", "reload"}},
						"skill_id":       map[string]any{"type": "string"},
						"script_name":    map[string]any{"type": "string"},
						"asset_name":     map[string]any{"type": "string"},
						"reference_name": map[string]any{"type": "string"},
						"sub_path":       map[string]any{"type": "string"},
						"content":        map[string]any{"type": "string"},
						"args":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"required": []string{"action"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "kv",
				Description: "Session KV store. action: get (returns null if missing) / set (overwrite, any JSON type) / append (add to array, creates if absent).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{"type": "string", "enum": []string{"get", "set", "append"}},
						"key":    map[string]any{"type": "string"},
						"value":  map[string]any{"description": "Required for set/append: any JSON value"},
					},
					"required": []string{"action", "key"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "get_current_user",
				Description: "Return the current logged-in user's user_id.",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "read_file",
				Description: "Extract text from uploaded .docx/.pptx/.xlsx. For PDF use read_pdf.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Uploaded file path"},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "tool_request",
				Description: "Manage missing tool requests. action: request / list / close. Call get_tool_doc(\"tool_request\") for full parameter docs.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{"type": "string", "enum": []string{"request", "list", "close"}},
					},
					"required":             []string{"action"},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "output_file",
				Description: "Write to output/ and return {path, download_url}. Supports text and Office (.docx/.xlsx/.pptx/.pdf, auto-converted by extension). action=download: URL for existing file. Call get_tool_doc(\"output_file\") for format details.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":    map[string]any{"type": "string", "enum": []string{"write", "download"}},
						"filename":  map[string]any{"type": "string", "description": "Output filename with extension"},
						"content":   map[string]any{"type": "string", "description": "Text content (write only)"},
						"file_path": map[string]any{"type": "string", "description": "Existing file path (download only)"},
					},
					"required": []string{"action"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "fs",
				Description: "File system ops. action: list / stat / read (images→multimodal; text max 512KB) / write (uploads/output/skills/ only) / delete / move / mkdir.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":    map[string]any{"type": "string", "enum": []string{"list", "stat", "read", "write", "delete", "move", "mkdir"}},
						"path":      map[string]any{"type": "string", "description": "Target path (required for all actions except move)"},
						"content":   map[string]any{"type": "string", "description": "write: file content"},
						"append":    map[string]any{"type": "boolean", "description": "write: true to append"},
						"recursive": map[string]any{"type": "boolean", "description": "delete: true to delete recursively"},
						"src":       map[string]any{"type": "string", "description": "move: source path"},
						"dst":       map[string]any{"type": "string", "description": "move: destination path"},
					},
					"required": []string{"action"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "exec",
				Description: "Execute a shell command (bash -c). Returns output or session_id for long-running commands. Call get_tool_doc(\"exec\") for advanced params (env, timeout_sec, yield_ms, background).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{"type": "string"},
						"workdir": map[string]any{"type": "string"},
					},
					"required":             []string{"command"},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "exec_run",
				Description: "Execute a user-approved exec command. pending_id expires in 5 minutes.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pending_id": map[string]any{"type": "string"},
					},
					"required": []string{"pending_id"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "process",
				Description: "Manage exec background processes (list/poll/log/write/submit/kill etc.). Many parameters — call get_tool_doc(\"process\") first for full parameter docs.",
				Parameters: map[string]any{
					"type":                 "object",
					"properties":          map[string]any{},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "send_file_upload",
				Description: "Show an image upload widget to the user. Stop after calling; retrieve the uploaded path or \"skip\" in the next turn.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":  map[string]any{"type": "string", "description": "Upload widget title"},
						"prompt": map[string]any{"type": "string", "description": "Additional description"},
					},
					"required": []string{"title"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "update_role_md",
				Description: "Overwrite config/ROLE.md and hot-reload (takes effect next turn). Irreversible. Pass finalize=true ONLY at final bootstrap step to lock initialized=true.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content":       map[string]any{"type": "string", "description": "New ROLE.md content"},
						"avatar_url":    map[string]any{"type": "string", "description": "Avatar file path (optional, e.g. uploads/3/abc.png)"},
						"extra_fs_dirs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Extra absolute paths users can access via fs"},
						"finalize":      map[string]any{"type": "boolean", "description": "Set true ONLY at final bootstrap step to lock initialized=true"},
					},
					"required": []string{"content"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "feishu",
				Description: "Feishu operations. action: send (Bot API, receive_id=\"self\" for bound user) / webhook (group Webhook, no credentials) / get_config (read config) / set_config (write App ID/Secret/Webhook/open_id). Call get_tool_doc(\"feishu\") for full parameter docs.",
				Parameters: map[string]any{
					"type":                 "object",
					"properties":          map[string]any{},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "wecom",
				Description: "WeCom operations. action: send (Webhook, msgtype text/markdown, omit webhook_url to use stored) / get_config (read masked URL) / set_config (save Webhook URL).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":      map[string]any{"type": "string", "enum": []string{"send", "get_config", "set_config"}},
						"text":        map[string]any{"type": "string", "description": "send: message content"},
						"msgtype":     map[string]any{"type": "string", "enum": []string{"text", "markdown"}, "description": "send: default text"},
						"webhook_url": map[string]any{"type": "string", "description": "send: optional (uses stored if omitted); set_config: required"},
					},
					"required": []string{"action"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "browser",
				Description: "Headless Chromium browser automation. MUST call get_tool_doc(\"browser\") before use for full params and login-handling protocol. Key rules: (1) snapshot=read page content, NEVER screenshot for that; (2) if login/verification detected, do NOT screenshot — use notify(options) to ask if server runs locally; if yes: close → launch(visible=true) → navigate to login page → wait for user → close visible browser → launch() headless → navigate to task URL → continue task. CRITICAL: visible browser is for login ONLY — never perform task work in it; always close and relaunch headless before continuing. visible=true only valid on launch.",
				Parameters: map[string]any{
					"type":                 "object",
					"properties":          map[string]any{},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "web_fetch",
				Description: "Fetch URL content via HTTP GET, returning Markdown/JSON/plain text. 5-minute cache. Prefer for static pages; use browser when JS rendering is needed.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url": map[string]any{
							"type":        "string",
							"description": "Target URL",
						},
						"max_chars": map[string]any{
							"type":        "integer",
							"description": "Max chars to return (default: 20000)",
						},
					},
					"required": []string{"url"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "read_pdf",
				Description: "Extract PDF text page by page. Supports page range (pages=\"1-5\") and image rendering (render=true for scanned documents).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "PDF path (uploads/ or output/ only)",
						},
						"pages": map[string]any{
							"type":        "string",
							"description": "Page range e.g. 1-5 or 1,3,7-10 (omit for all)",
						},
						"render": map[string]any{
							"type":        "boolean",
							"description": "Render pages as images (for scanned docs)",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "code_search",
				Description: "Explore codebase (tree for directory listing / grep for content search). Call get_tool_doc(\"code_search\") first for full parameter docs.",
				Parameters: map[string]any{
					"type":                 "object",
					"properties":          map[string]any{},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "cron",
				Description: "Manage scheduled tasks (add/list/update/remove/run/status). The schedule field is complex — call get_tool_doc(\"cron\") first for full parameter docs.",
				Parameters: map[string]any{
					"type":                 "object",
					"properties":          map[string]any{},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "user_persona",
				Description: "Get or set user persona (name, language, style). action: get / set.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":  map[string]any{"type": "string", "enum": []string{"get", "set"}},
						"persona": map[string]any{"type": "string", "description": "Required for set"},
					},
					"required": []string{"action"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "nano_banana",
				Description: "Generate images using the nano-banana-pro model (txt2img/img2img/edit). Call get_tool_doc(\"nano_banana\") first for full parameter docs.",
				Parameters: map[string]any{
					"type":                 "object",
					"properties":          map[string]any{},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "get_tool_doc",
				Description: "Retrieve detailed usage documentation for a tool (parameters, examples, notes). Call when unsure about tool usage.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Tool name",
						},
					},
					"required": []string{"name"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "read_image",
				Description: "View an image for visual analysis. History images stored as [file: path]; find path before calling.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Image file path",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "mcp",
				Description: "Access external MCP servers. action=list: show all servers and their tools; action=detail: get full schema for a specific tool; action=call: execute a tool. Call get_tool_doc(\"mcp\") for details.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type": "string",
							"enum": []string{"list", "detail", "call"},
						},
						"server": map[string]any{
							"type":        "string",
							"description": "MCP server name (required for action=detail/call)",
						},
						"tool": map[string]any{
							"type":        "string",
							"description": "Tool name within the server (required for action=detail/call)",
						},
						"args": map[string]any{
							"type":        "object",
							"description": "Tool arguments as key-value pairs (required for action=call)",
						},
					},
					"required": []string{"action"},
				},
			},
		},
	}
	// 按开关追加长期记忆工具
	if config.Cfg.MemoryEnabled {
		all = append(all, MemoryTool())
	}
	if config.Cfg.SessionSearchEnabled {
		all = append(all, llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "session_search",
				Description: "Search past sessions (with query: FTS + AI summary, limit 1-5) or list recent sessions (no query: metadata only, limit 1-10). Use for context from earlier conversations only.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Keywords to search. Omit to list recent sessions.",
						},
						"limit": map[string]any{
							"type":        "integer",
							"description": "Max results (with query: default 3 max 5; no query: default 5 max 10)",
						},
					},
					"required": []string{},
				},
			},
		})
	}
	if config.Cfg.HonchoEnabled {
		all = append(all, HonchoTools()...)
	}
	// initialized=true 后，过滤掉仅在 bootstrap 阶段使用的工具
	if cfg, err := storage.GetAppConfig(); err == nil && cfg.Initialized {
		bootstrapOnly := map[string]bool{"update_role_md": true, "send_file_upload": true}
		filtered := make([]llm.Tool, 0, len(all))
		for _, t := range all {
			if !bootstrapOnly[t.Function.Name] {
				filtered = append(filtered, t)
			}
		}
		return filtered
	}
	return all
}

// ========== 工具实现 ==========

// handleSendProgress 通过 context 注入的 ProgressSender 推送进度给前端
func handleSendProgress(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse send_progress args: %w", err)
	}
	if args.Message == "" {
		return "", fmt.Errorf("message is required")
	}
	sender := senderFromCtx(ctx)
	if sender == nil {
		return "ok", nil // 无前端连接（如测试），静默跳过
	}
	if err := sender(args.Message); err != nil {
		return "", fmt.Errorf("push progress to frontend: %w", err)
	}
	return "ok", nil
}

// handleSendOptions 向前端推送选项列表，等待用户选择
func handleSendOptions(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Title   string `json:"title"`
		Options []struct {
			Label string `json:"label"`
			Value string `json:"value"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse send_options args: %w", err)
	}
	if args.Title == "" {
		return "", fmt.Errorf("title is required")
	}
	if len(args.Options) == 0 {
		return "", fmt.Errorf("options must not be empty")
	}

	sender := interactiveSenderFromCtx(ctx)
	if sender == nil {
		return "ok", nil
	}
	if err := sender("options", args); err != nil {
		return "", fmt.Errorf("push options to frontend: %w", err)
	}
	return "ok", nil
}

// handleSendConfirm 向前端推送确认框，等待用户确认
func handleSendConfirm(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Message      string `json:"message"`
		ConfirmLabel string `json:"confirm_label"`
		CancelLabel  string `json:"cancel_label"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse send_confirm args: %w", err)
	}
	if args.Message == "" {
		return "", fmt.Errorf("message is required")
	}
	// 填充默认按钮文案
	if args.ConfirmLabel == "" {
		args.ConfirmLabel = "确认"
	}
	if args.CancelLabel == "" {
		args.CancelLabel = "取消"
	}

	sender := interactiveSenderFromCtx(ctx)
	if sender == nil {
		return "ok", nil
	}
	if err := sender("confirm", args); err != nil {
		return "", fmt.Errorf("push confirm to frontend: %w", err)
	}
	return "ok", nil
}

// handleNotify 通过 action 字段分发，替代 send_progress / send_options / send_confirm 三个工具。
// action: progress / options / confirm
func handleNotify(ctx context.Context, argsJSON string) (string, error) {
	var base struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &base); err != nil {
		return "", fmt.Errorf("parse notify action: %w", err)
	}
	switch base.Action {
	case "progress":
		return handleSendProgress(ctx, argsJSON)
	case "options":
		return handleSendOptions(ctx, argsJSON)
	case "confirm":
		return handleSendConfirm(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown notify action: %q (valid: progress/options/confirm)", base.Action)
	}
}

// handleSendFileUpload 向前端推送文件上传控件（仅支持图片）。
// 前端完成上传后将文件路径作为下一轮用户消息发送；若用户跳过则发送 "skip"。
func handleSendFileUpload(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Title  string `json:"title"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse send_file_upload args: %w", err)
	}
	if args.Title == "" {
		args.Title = "上传图片"
	}
	sender := interactiveSenderFromCtx(ctx)
	if sender == nil {
		return "ok", nil
	}
	if err := sender("upload", args); err != nil {
		return "", fmt.Errorf("push upload widget to frontend: %w", err)
	}
	return "ok", nil
}


// handleGetSkillContent 读取指定 skill_id 的完整内容，并向前端推送技能的优雅名称。
func handleGetSkillContent(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SkillID string `json:"skill_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse get_skill_content args: %w", err)
	}
	if args.SkillID == "" {
		return "", fmt.Errorf("skill_id is required")
	}

	// bootstrap 技能在系统已初始化后硬锁定，防止意外触发覆盖现有角色配置。
	// 若需要重置系统，管理员须手动将 config/app.json 中的 initialized 改为 false。
	if args.SkillID == "bootstrap" {
		if appCfg, err := storage.GetAppConfig(); err == nil && appCfg.Initialized {
			return "", fmt.Errorf("bootstrap skill is locked: system is already initialized. " +
				"To reset the system, an administrator must manually set initialized=false in config/app.json.")
		}
	}

	userID := userIDFromCtx(ctx)
	content, err := skill.Store.GetContent(userID, args.SkillID)
	if err != nil {
		return "", err
	}
	// 通知前端切换显示名称：优先使用 display_name，回退到 name
	if h, ok := skill.Store.GetSkillHead(userID, args.SkillID); ok {
		name := h.DisplayName
		if name == "" {
			name = h.Name
		}
		if sender := speakerSenderFromCtx(ctx); sender != nil {
			_ = sender(name)
		}
	}
	// Approximate LFU usage tracking: record when a self-improving skill is loaded.
	if skillDir, ok := skill.Store.GetSkillDir(userID, args.SkillID); ok {
		siMarker := filepath.Join("self-improving", "skills")
		if strings.Contains(skillDir, siMarker) {
			userSkillsDir := skill.Store.GetUserSkillsDir(userID)
			decayHours := config.Cfg.SelfImprovingLFUDecayHours
			go skill.RecordSelfImprovingUse(userSkillsDir, args.SkillID, decayHours)
		}
	}
	return content, nil
}

// handleRunScript 执行技能 script/ 目录下的脚本，返回合并后的标准输出
//
// 安全约束：
//   - 脚本路径严格限制在 {skill_root}/script/ 目录内，拒绝路径穿越
//   - 执行超时 60 秒
//
// 解释器选择（按扩展名）：
//
//	.sh  → bash
//	.py  → python3
//	.js  → node
//	其他 → 直接执行（需要可执行权限 + shebang）
func handleRunScript(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SkillID    string   `json:"skill_id"`
		ScriptName string   `json:"script_name"`
		Args       []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse run_script args: %w", err)
	}
	if args.SkillID == "" {
		return "", fmt.Errorf("skill_id is required")
	}
	if args.ScriptName == "" {
		return "", fmt.Errorf("script_name is required")
	}

	skillDir, ok := skill.Store.GetSkillDir(userIDFromCtx(ctx), args.SkillID)
	if !ok {
		return "", fmt.Errorf("skill %q not found", args.SkillID)
	}

	// 构造并校验脚本绝对路径，防止路径穿越
	scriptDir := filepath.Join(skillDir, "script")
	scriptPath := filepath.Clean(filepath.Join(scriptDir, args.ScriptName))

	absScriptDir, err := filepath.Abs(scriptDir)
	if err != nil {
		return "", fmt.Errorf("resolve script dir: %w", err)
	}
	absScriptPath, err := filepath.Abs(scriptPath)
	if err != nil {
		return "", fmt.Errorf("resolve script path: %w", err)
	}
	if !strings.HasPrefix(absScriptPath, absScriptDir+string(filepath.Separator)) {
		return "", fmt.Errorf("script path escapes skill script directory")
	}

	if _, err := os.Stat(absScriptPath); os.IsNotExist(err) {
		return "", fmt.Errorf("script %q not found in skill %q", args.ScriptName, args.SkillID)
	}

	// 按扩展名选择解释器
	var cmdArgs []string
	switch strings.ToLower(filepath.Ext(args.ScriptName)) {
	case ".sh":
		cmdArgs = append([]string{"bash", absScriptPath}, args.Args...)
	case ".py":
		cmdArgs = append([]string{"python3", absScriptPath}, args.Args...)
	case ".js":
		cmdArgs = append([]string{"node", absScriptPath}, args.Args...)
	default:
		cmdArgs = append([]string{absScriptPath}, args.Args...)
	}

	// 带超时的 context
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(config.Cfg.ToolScriptTimeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = skillDir // 工作目录设为技能根目录

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("script timed out after %ds", config.Cfg.ToolScriptTimeoutSec)
		}
		errOut := strings.TrimSpace(stderr.String())
		if errOut == "" {
			return "", fmt.Errorf("script exited with error: %v", err)
		}
		return "", fmt.Errorf("script exited with error: %v\nstderr: %s", err, errOut)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// handleReadAsset 读取技能 assets/ 目录下的参考文件，返回文件内容字符串
//
// 安全约束：文件路径严格限制在 {skill_root}/assets/ 目录内，拒绝路径穿越
func handleReadAsset(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SkillID   string `json:"skill_id"`
		AssetName string `json:"asset_name"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse read_asset args: %w", err)
	}
	if args.SkillID == "" {
		return "", fmt.Errorf("skill_id is required")
	}
	if args.AssetName == "" {
		return "", fmt.Errorf("asset_name is required")
	}

	skillDir, ok := skill.Store.GetSkillDir(userIDFromCtx(ctx), args.SkillID)
	if !ok {
		return "", fmt.Errorf("skill %q not found", args.SkillID)
	}

	// 构造并校验资产绝对路径，防止路径穿越
	assetsDir := filepath.Join(skillDir, "assets")
	assetPath := filepath.Clean(filepath.Join(assetsDir, args.AssetName))

	absAssetsDir, err := filepath.Abs(assetsDir)
	if err != nil {
		return "", fmt.Errorf("resolve assets dir: %w", err)
	}
	absAssetPath, err := filepath.Abs(assetPath)
	if err != nil {
		return "", fmt.Errorf("resolve asset path: %w", err)
	}
	if !strings.HasPrefix(absAssetPath, absAssetsDir+string(filepath.Separator)) {
		return "", fmt.Errorf("asset path escapes skill assets directory")
	}

	data, err := os.ReadFile(absAssetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("asset %q not found in skill %q", args.AssetName, args.SkillID)
		}
		return "", fmt.Errorf("read asset %q: %w", args.AssetName, err)
	}

	return string(data), nil
}

// handleReadReference 读取技能 references/ 目录下的参考文件，返回文件内容字符串
//
// 安全约束：文件路径严格限制在 {skill_root}/references/ 目录内，拒绝路径穿越
func handleReadReference(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SkillID       string `json:"skill_id"`
		ReferenceName string `json:"reference_name"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse read_reference args: %w", err)
	}
	if args.SkillID == "" {
		return "", fmt.Errorf("skill_id is required")
	}
	if args.ReferenceName == "" {
		return "", fmt.Errorf("reference_name is required")
	}

	skillDir, ok := skill.Store.GetSkillDir(userIDFromCtx(ctx), args.SkillID)
	if !ok {
		return "", fmt.Errorf("skill %q not found", args.SkillID)
	}

	// 构造并校验参考文件绝对路径，防止路径穿越
	referencesDir := filepath.Join(skillDir, "references")
	refPath := filepath.Clean(filepath.Join(referencesDir, args.ReferenceName))

	absReferencesDir, err := filepath.Abs(referencesDir)
	if err != nil {
		return "", fmt.Errorf("resolve references dir: %w", err)
	}
	absRefPath, err := filepath.Abs(refPath)
	if err != nil {
		return "", fmt.Errorf("resolve reference path: %w", err)
	}
	if !strings.HasPrefix(absRefPath, absReferencesDir+string(filepath.Separator)) {
		return "", fmt.Errorf("reference path escapes skill references directory")
	}

	data, err := os.ReadFile(absRefPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("reference %q not found in skill %q", args.ReferenceName, args.SkillID)
		}
		return "", fmt.Errorf("read reference %q: %w", args.ReferenceName, err)
	}

	return string(data), nil
}

// handleKVGet 从会话 KV 中读取单个键的值，返回 JSON 字符串
// 键不存在时返回 "null"
func handleKVGet(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse kv_get args: %w", err)
	}
	if args.Key == "" {
		return "", fmt.Errorf("key is required")
	}

	sessionID := sessionIDFromCtx(ctx)
	if sessionID == "" {
		return "", fmt.Errorf("no session in context")
	}

	kv, err := storage.GetSessionKV(sessionID)
	if err != nil {
		return "", fmt.Errorf("get session kv: %w", err)
	}

	val, ok := kv[args.Key]
	if !ok {
		return "null", nil
	}

	b, err := json.Marshal(val)
	if err != nil {
		return "", fmt.Errorf("marshal kv value: %w", err)
	}
	return string(b), nil
}

// handleKVSet 向会话 KV 写入单个键值对
// value 接受任意 JSON 类型（string / number / object / array / bool / null）
func handleKVSet(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"` // 保留原始 JSON，支持任意类型
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse kv_set args: %w", err)
	}
	if args.Key == "" {
		return "", fmt.Errorf("key is required")
	}
	if len(args.Value) == 0 {
		return "", fmt.Errorf("value is required")
	}

	sessionID := sessionIDFromCtx(ctx)
	if sessionID == "" {
		return "", fmt.Errorf("no session in context")
	}

	// 反序列化 value 为 interface{} 以便存入 map
	var val interface{}
	if err := json.Unmarshal(args.Value, &val); err != nil {
		return "", fmt.Errorf("invalid value JSON: %w", err)
	}

	kv, err := storage.GetSessionKV(sessionID)
	if err != nil {
		return "", fmt.Errorf("get session kv: %w", err)
	}

	kv[args.Key] = val

	if err := storage.UpdateSessionKV(sessionID, kv); err != nil {
		return "", fmt.Errorf("update session kv: %w", err)
	}
	return "ok", nil
}

// handleKVAppend 向会话 KV 的某个键追加元素（累积语义）
//
// 三种情况：
//   - 键不存在           → 创建 [value]
//   - 键已是 []interface{} → 追加到末尾
//   - 键是其他类型        → 包装为 [旧值, 新值]，避免数据丢失
func handleKVAppend(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse kv_append args: %w", err)
	}
	if args.Key == "" {
		return "", fmt.Errorf("key is required")
	}
	if len(args.Value) == 0 {
		return "", fmt.Errorf("value is required")
	}

	sessionID := sessionIDFromCtx(ctx)
	if sessionID == "" {
		return "", fmt.Errorf("no session in context")
	}

	var newItem interface{}
	if err := json.Unmarshal(args.Value, &newItem); err != nil {
		return "", fmt.Errorf("invalid value JSON: %w", err)
	}

	kv, err := storage.GetSessionKV(sessionID)
	if err != nil {
		return "", fmt.Errorf("get session kv: %w", err)
	}

	existing, exists := kv[args.Key]
	switch {
	case !exists:
		// 键不存在：初始化为单元素数组
		kv[args.Key] = []interface{}{newItem}
	case isSlice(existing):
		// 键已是数组：追加
		kv[args.Key] = append(existing.([]interface{}), newItem)
	default:
		// 键是其他类型：包装为 [旧值, 新值]，防止数据丢失
		kv[args.Key] = []interface{}{existing, newItem}
	}

	if err := storage.UpdateSessionKV(sessionID, kv); err != nil {
		return "", fmt.Errorf("update session kv: %w", err)
	}
	return "ok", nil
}

// isSlice 判断 interface{} 是否是 []interface{}
// JSON 反序列化后数组类型固定为 []interface{}
func isSlice(v interface{}) bool {
	_, ok := v.([]interface{})
	return ok
}

// handleKv 通过 action 字段分发，替代 kv_get / kv_set / kv_append 三个工具。
// action: get / set / append
func handleKv(ctx context.Context, argsJSON string) (string, error) {
	var base struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &base); err != nil {
		return "", fmt.Errorf("parse kv action: %w", err)
	}
	switch base.Action {
	case "get":
		return handleKVGet(ctx, argsJSON)
	case "set":
		return handleKVSet(ctx, argsJSON)
	case "append":
		return handleKVAppend(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown kv action: %q (valid: get/set/append)", base.Action)
	}
}

// resolveSkillBaseDir 根据初始化状态决定技能写入的根目录：
//   - initialized=false（初始化阶段）→ skills/system/（system skill，所有用户可见）
//   - initialized=true（正式运行）    → skills/users/{userID}/（用户个人 skill）
func resolveSkillBaseDir(userID string) string {
	appCfg, _ := storage.GetAppConfig()
	if !appCfg.Initialized {
		return skill.Store.GetSystemSkillsDir()
	}
	return skill.Store.GetUserSkillsDir(userID)
}

// handleWriteSkillFile 创建技能文件夹和 SKILL.md（或脚本/资源文件），写入位置由 resolveSkillBaseDir 决定。
//
// 参数：
//   - skill_id: 技能唯一标识，只允许小写字母、数字、下划线
//   - content: 文件内容
//   - sub_path（可选）: 相对于技能根目录的路径：
//     - 省略 → 写入 SKILL.md（默认行为）
//     - "script/foo.sh"       → 写入 script/ 目录
//     - "assets/bar.json"     → 写入 assets/ 目录（资产文件）
//     - "references/baz.md"   → 写入 references/ 目录（参考文件）
//
// 安全约束：
//   - skill_id 只允许小写字母、数字、下划线（防止路径注入）
//   - sub_path 仅允许 script/、assets/ 或 references/ 前缀，防止路径穿越
//   - 写入 SKILL.md 时不允许覆盖已存在的技能
func handleWriteSkillFile(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SkillID string `json:"skill_id"`
		Content string `json:"content"`
		SubPath string `json:"sub_path"` // 可选：省略则写 SKILL.md
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse write_skill_file args: %w", err)
	}
	if args.SkillID == "" {
		return "", fmt.Errorf("skill_id is required")
	}
	if args.Content == "" {
		return "", fmt.Errorf("content is required")
	}
	if !skillIDRegex.MatchString(args.SkillID) {
		return "", fmt.Errorf("skill_id %q is invalid: only lowercase letters, digits, and underscores are allowed", args.SkillID)
	}

	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}
	skillsBaseDir := resolveSkillBaseDir(userID)
	if skillsBaseDir == "" {
		return "", fmt.Errorf("skills directory not initialized")
	}

	absBaseDir, err := filepath.Abs(skillsBaseDir)
	if err != nil {
		return "", fmt.Errorf("resolve skills base dir: %w", err)
	}
	absSkillDir := filepath.Clean(filepath.Join(absBaseDir, args.SkillID))
	if !strings.HasPrefix(absSkillDir, absBaseDir+string(filepath.Separator)) {
		return "", fmt.Errorf("skill_id escapes skills base directory")
	}

	// sub_path 为 "SKILL.md" 时自动纠正为空（LLM 常见误用）
	if args.SubPath == "SKILL.md" || strings.EqualFold(args.SubPath, "skill.md") {
		args.SubPath = ""
	}

	// 无 sub_path → 写 SKILL.md（原有逻辑）
	if args.SubPath == "" {
		parsed, err := skill.ParseContent(args.Content)
		if err != nil {
			return "", fmt.Errorf("SKILL.md 格式错误：%w\n提示：请先调用 skill(action=read_asset, skill_id=skill_creator, asset_name=skill_template.md) 获取标准格式模板，再重新写入", err)
		}
		if parsed.SkillID != args.SkillID {
			return "", fmt.Errorf("内容中的 skill_id=%q 与参数 skill_id=%q 不一致，请保持一致", parsed.SkillID, args.SkillID)
		}

		if err := skill.ScanSkillFile(args.Content, ""); err != nil {
			return "", fmt.Errorf("skill security scan rejected SKILL.md: %w", err)
		}
		mdPath := filepath.Join(absSkillDir, "SKILL.md")
		if _, statErr := os.Stat(mdPath); statErr == nil {
			return "", fmt.Errorf("skill %q already exists; overwriting is not allowed", args.SkillID)
		}
		if err := os.MkdirAll(absSkillDir, 0o755); err != nil {
			return "", fmt.Errorf("create skill directory: %w", err)
		}
		if err := os.WriteFile(mdPath, []byte(args.Content), 0o644); err != nil {
			return "", fmt.Errorf("write SKILL.md: %w", err)
		}
		return fmt.Sprintf("SKILL.md written to %s", mdPath), nil
	}

	// 有 sub_path → 只允许 script/、assets/ 或 references/ 前缀
	if !strings.HasPrefix(args.SubPath, "script/") && !strings.HasPrefix(args.SubPath, "assets/") && !strings.HasPrefix(args.SubPath, "references/") {
		hint := ""
		if args.SubPath == "SKILL.md" || strings.EqualFold(args.SubPath, "skill.md") {
			hint = " (to write SKILL.md, omit sub_path entirely)"
		}
		return "", fmt.Errorf("sub_path must start with \"script/\", \"assets/\", or \"references/\", got: %q%s", args.SubPath, hint)
	}

	// 辅助文件写入时，优先使用技能在 store 中的实际 RootPath。
	// 这样 self-improving/skills/{id}/ 和普通 users/{userID}/{id}/ 都能正确定位，
	// 无需在此处硬编码具体目录结构。
	// 技能不存在时（尚未 reload）回退到 resolveSkillBaseDir 拼接路径。
	if actualSkillDir, ok := skill.Store.GetSkillDir(userID, args.SkillID); ok {
		absActualSkillDir, err := filepath.Abs(actualSkillDir)
		if err != nil {
			return "", fmt.Errorf("resolve skill dir: %w", err)
		}
		absSkillDir = absActualSkillDir
	}

	targetPath := filepath.Clean(filepath.Join(absSkillDir, args.SubPath))
	absTargetPath, err := filepath.Abs(targetPath)
	if err != nil {
		return "", fmt.Errorf("resolve target path: %w", err)
	}
	if !strings.HasPrefix(absTargetPath, absSkillDir+string(filepath.Separator)) {
		return "", fmt.Errorf("sub_path escapes skill directory")
	}

	if err := skill.ScanSkillFile(args.Content, args.SubPath); err != nil {
		return "", fmt.Errorf("skill security scan rejected %s: %w", args.SubPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(absTargetPath), 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}
	if err := os.WriteFile(absTargetPath, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return fmt.Sprintf("file written to %s", absTargetPath), nil
}

// handleWriteSkillScript 在技能的 script/ 目录下创建或更新脚本文件。
//
// 安全约束：
//   - skill_id 只允许小写字母、数字、下划线
//   - script_name 严格限制在 {skill_root}/script/ 目录内，拒绝路径穿越
func handleWriteSkillScript(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SkillID    string `json:"skill_id"`
		ScriptName string `json:"script_name"`
		Content    string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse write_skill_script args: %w", err)
	}
	if args.SkillID == "" {
		return "", fmt.Errorf("skill_id is required")
	}
	if args.ScriptName == "" {
		return "", fmt.Errorf("script_name is required")
	}
	if args.Content == "" {
		return "", fmt.Errorf("content is required")
	}
	if !skillIDRegex.MatchString(args.SkillID) {
		return "", fmt.Errorf("skill_id %q is invalid: only lowercase letters, digits, and underscores are allowed", args.SkillID)
	}

	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}
	skillsBaseDir := resolveSkillBaseDir(userID)
	if skillsBaseDir == "" {
		return "", fmt.Errorf("skills directory not initialized")
	}

	absBaseDir, err := filepath.Abs(skillsBaseDir)
	if err != nil {
		return "", fmt.Errorf("resolve skills base dir: %w", err)
	}

	absSkillDir := filepath.Join(absBaseDir, args.SkillID)
	if actualSkillDir, ok := skill.Store.GetSkillDir(userID, args.SkillID); ok {
		if abs, err := filepath.Abs(actualSkillDir); err == nil {
			absSkillDir = abs
		}
	}

	scriptDir := filepath.Join(absSkillDir, "script")
	scriptPath := filepath.Clean(filepath.Join(scriptDir, args.ScriptName))

	absScriptDir, err := filepath.Abs(scriptDir)
	if err != nil {
		return "", fmt.Errorf("resolve script dir: %w", err)
	}
	absScriptPath, err := filepath.Abs(scriptPath)
	if err != nil {
		return "", fmt.Errorf("resolve script path: %w", err)
	}
	if !strings.HasPrefix(absScriptPath, absScriptDir+string(filepath.Separator)) {
		return "", fmt.Errorf("script_name escapes script directory")
	}

	if err := os.MkdirAll(absScriptDir, 0o755); err != nil {
		return "", fmt.Errorf("create script directory: %w", err)
	}
	if err := os.WriteFile(absScriptPath, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write script file: %w", err)
	}
	return fmt.Sprintf("script written to %s", absScriptPath), nil
}

// handleWriteSkillAsset 在技能的 assets/ 目录下创建或更新参考文件。
//
// 安全约束：
//   - skill_id 只允许小写字母、数字、下划线
//   - asset_name 严格限制在 {skill_root}/assets/ 目录内，拒绝路径穿越
func handleWriteSkillAsset(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SkillID   string `json:"skill_id"`
		AssetName string `json:"asset_name"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse write_skill_asset args: %w", err)
	}
	if args.SkillID == "" {
		return "", fmt.Errorf("skill_id is required")
	}
	if args.AssetName == "" {
		return "", fmt.Errorf("asset_name is required")
	}
	if args.Content == "" {
		return "", fmt.Errorf("content is required")
	}
	if !skillIDRegex.MatchString(args.SkillID) {
		return "", fmt.Errorf("skill_id %q is invalid: only lowercase letters, digits, and underscores are allowed", args.SkillID)
	}

	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}
	skillsBaseDir := resolveSkillBaseDir(userID)
	if skillsBaseDir == "" {
		return "", fmt.Errorf("skills directory not initialized")
	}

	absBaseDir, err := filepath.Abs(skillsBaseDir)
	if err != nil {
		return "", fmt.Errorf("resolve skills base dir: %w", err)
	}

	absSkillDir := filepath.Join(absBaseDir, args.SkillID)
	if actualSkillDir, ok := skill.Store.GetSkillDir(userID, args.SkillID); ok {
		if abs, err := filepath.Abs(actualSkillDir); err == nil {
			absSkillDir = abs
		}
	}

	assetsDir := filepath.Join(absSkillDir, "assets")
	assetPath := filepath.Clean(filepath.Join(assetsDir, args.AssetName))

	absAssetsDir, err := filepath.Abs(assetsDir)
	if err != nil {
		return "", fmt.Errorf("resolve assets dir: %w", err)
	}
	absAssetPath, err := filepath.Abs(assetPath)
	if err != nil {
		return "", fmt.Errorf("resolve asset path: %w", err)
	}
	if !strings.HasPrefix(absAssetPath, absAssetsDir+string(filepath.Separator)) {
		return "", fmt.Errorf("asset_name escapes assets directory")
	}

	if err := os.MkdirAll(absAssetsDir, 0o755); err != nil {
		return "", fmt.Errorf("create assets directory: %w", err)
	}
	if err := os.WriteFile(absAssetPath, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write asset file: %w", err)
	}
	return fmt.Sprintf("asset written to %s", absAssetPath), nil
}

// handleReloadSkills 重新扫描 skills 目录，原子替换 Store 中的 map，并同步工具需求状态。
// 新增技能文件后立即调用此工具可使新技能生效，无需重启服务。
func (e *Executor) handleReloadSkills(_ context.Context, _ string) (string, error) {
	if err := skill.Store.Reload(); err != nil {
		return "", fmt.Errorf("reload skills: %w", err)
	}
	if err := e.SyncToolRequests(); err != nil {
		return "", fmt.Errorf("sync tool request status: %w", err)
	}
	return "ok", nil
}

// handleSkill 通过 action 字段分发，替代 5 个独立工具：
// get_skill_content / run_script / read_asset / read_reference / write_skill_file / reload_skills
// action: load / run_script / read_asset / read_reference / write / reload
func (e *Executor) handleSkill(ctx context.Context, argsJSON string) (string, error) {
	var base struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &base); err != nil {
		return "", fmt.Errorf("parse skill action: %w", err)
	}
	switch base.Action {
	case "load":
		return handleGetSkillContent(ctx, argsJSON)
	case "run_script":
		return handleRunScript(ctx, argsJSON)
	case "read_asset":
		return handleReadAsset(ctx, argsJSON)
	case "read_reference":
		return handleReadReference(ctx, argsJSON)
	case "write":
		return handleWriteSkillFile(ctx, argsJSON)
	case "reload":
		return e.handleReloadSkills(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown skill action: %q (valid: load/run_script/read_asset/read_reference/write/reload)", base.Action)
	}
}

// handleUpdateRoleMD 写入新的 ROLE.md 内容并通过 context 注入的 RoleUpdater 热加载。
// 写入后 Agent singleton 的 roleMD 缓存立即更新，下一轮对话使用新角色。
// 仅当 finalize=true 时才将 app.json 的 initialized 置为 true，锁定初始化入口。
func handleUpdateRoleMD(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Content     string   `json:"content"`
		AvatarURL   string   `json:"avatar_url"`
		ExtraFsDirs []string `json:"extra_fs_dirs"`
		Finalize    bool     `json:"finalize"` // true → bootstrap 最后一步，写完后锁定 initialized
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse update_role_md args: %w", err)
	}
	if strings.TrimSpace(args.Content) == "" {
		return "", fmt.Errorf("content is required")
	}

	// 初始化完成后 ROLE.md 锁定，禁止通过工具覆盖
	if cfg, err := storage.GetAppConfig(); err == nil && cfg.Initialized {
		return "", fmt.Errorf("系统已完成初始化，ROLE.md 已锁定，无法通过工具修改（如需重置，请联系管理员手动修改 config/app.json）")
	}

	// 可选：同步写入头像 URL（失败不阻断主流程）
	if args.AvatarURL != "" {
		_ = storage.SetAvatarURL(args.AvatarURL)
	}

	// 可选：写入 fs 工具额外可读目录（失败不阻断主流程）
	if args.ExtraFsDirs != nil {
		_ = storage.SetExtraFsDirs(args.ExtraFsDirs)
	}

	updater := roleUpdaterFromCtx(ctx)
	if updater == nil {
		return "", fmt.Errorf("role updater not available in context")
	}
	if err := updater(args.Content); err != nil {
		return "", fmt.Errorf("update ROLE.md: %w", err)
	}
	// 仅在 bootstrap 最后一步（finalize=true）时锁定初始化入口，确保所有系统技能均已创建完毕
	if args.Finalize {
		_ = storage.MarkInitialized()
	}
	return "ROLE.md 已写入并热加载，新角色从下一轮对话起生效", nil
}

// handleGetCurrentUser 返回当前登录用户的 user_id。
func handleGetCurrentUser(ctx context.Context, _ string) (string, error) {
	uid := userIDFromCtx(ctx)
	if uid == "" {
		return "", fmt.Errorf("user_id not available in context")
	}
	return uid, nil
}

// handleRequestTool 记录 LLM 需要但尚未实现的工具到数据库。
func handleRequestTool(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Trigger      string `json:"trigger"`
		InputSchema  string `json:"input_schema"`
		OutputSchema string `json:"output_schema"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse request_tool args: %w", err)
	}
	if strings.TrimSpace(args.Name) == "" || strings.TrimSpace(args.Description) == "" {
		return "", fmt.Errorf("name and description are required")
	}
	if err := storage.CreateToolRequest(&storage.ToolRequest{
		Name:         strings.TrimSpace(args.Name),
		Description:  args.Description,
		Trigger:      args.Trigger,
		InputSchema:  args.InputSchema,
		OutputSchema: args.OutputSchema,
		UserID:       userIDFromCtx(ctx),
		SessionID:    sessionIDFromCtx(ctx),
	}); err != nil {
		return "", fmt.Errorf("save tool request: %w", err)
	}
	return fmt.Sprintf("已记录工具需求「%s」，开发者将在后台跟进。", args.Name), nil
}

// handleListToolRequests 查询历史工具需求记录，支持按 status 过滤。
// 每次调用前先同步工具实现状态，确保返回的是最新数据。
func (e *Executor) handleListToolRequests(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Status string `json:"status"`
	}
	if argsJSON != "" && argsJSON != "{}" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse list_tool_requests args: %w", err)
		}
	}
	if err := e.SyncToolRequests(); err != nil {
		return "", fmt.Errorf("sync tool request status: %w", err)
	}
	rows, err := storage.ListToolRequests(args.Status)
	if err != nil {
		return "", fmt.Errorf("list tool requests: %w", err)
	}
	if len(rows) == 0 {
		if args.Status == "pending" {
			return "暂无未实现的工具需求。", nil
		}
		return "暂无工具需求记录。", nil
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return "", fmt.Errorf("marshal tool requests: %w", err)
	}
	return string(b), nil
}

// handleCloseToolRequest 将指定工具需求标记为 done，记录关闭原因。
func handleCloseToolRequest(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		ID     uint   `json:"id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse close_tool_request args: %w", err)
	}
	if args.ID == 0 {
		return "", fmt.Errorf("id is required")
	}
	if strings.TrimSpace(args.Reason) == "" {
		return "", fmt.Errorf("reason is required")
	}
	if err := storage.CloseToolRequest(args.ID, args.Reason); err != nil {
		return "", fmt.Errorf("close tool request: %w", err)
	}
	return fmt.Sprintf("工具需求 #%d 已标记为已实现，原因：%s", args.ID, args.Reason), nil
}

// handleToolRequest 通过 action 字段分发，替代 request_tool / list_tool_requests / close_tool_request 三个工具。
// action: request / list / close
func (e *Executor) handleToolRequest(ctx context.Context, argsJSON string) (string, error) {
	var base struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &base); err != nil {
		return "", fmt.Errorf("parse tool_request action: %w", err)
	}
	switch base.Action {
	case "request":
		return handleRequestTool(ctx, argsJSON)
	case "list":
		return e.handleListToolRequests(ctx, argsJSON)
	case "close":
		return handleCloseToolRequest(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown tool_request action: %q (valid: request/list/close)", base.Action)
	}
}

// handleReadFile 提取上传文件的文本内容（.docx / .pdf / .pptx / .xlsx）。
func handleReadFile(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse read_file args: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("path is required")
	}
	text, err := readUploadedFile(args.Path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "(文件中未提取到文本内容，可能是扫描件或加密文档)", nil
	}
	return text, nil
}

// handleGetToolDoc 按名称从 TOOL.md 中提取指定工具的详细文档
func handleGetToolDoc(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse get_tool_doc args: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	data, err := os.ReadFile(config.Cfg.ToolMDPath)
	if err != nil {
		return "", fmt.Errorf("read TOOL.md: %w", err)
	}
	content := string(data)

	// 查找 "## {name}" 段落，截取到下一个 "---" 或 "## " 或文件末尾
	header := "## " + args.Name
	idx := strings.Index(content, header+"\n")
	if idx < 0 {
		return fmt.Sprintf("未找到工具 %q 的文档。", args.Name), nil
	}

	section := content[idx+len(header)+1:]
	// 以 "---" 或下一个 "## " 作为结束标志
	endIdx := len(section)
	if i := strings.Index(section, "\n---"); i >= 0 && i < endIdx {
		endIdx = i
	}
	if i := strings.Index(section, "\n## "); i >= 0 && i < endIdx {
		endIdx = i
	}

	return strings.TrimSpace(section[:endIdx]), nil
}
