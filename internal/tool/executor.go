// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/executor.go — 工具注册与执行
//
// 内置工具（合并后）：
//   - read_file         : 统一文件读取入口（合并自 read_file/read_pdf/read_image），按扩展名自动路由
//   - notify            : UI 通知统一入口（action=progress/options/confirm/upload，合并自 send_progress/send_options/send_confirm/send_file_upload）
//     progress/upload 纯 UI，不写 DB；options/confirm 写 DB，下轮上下文可见
//   - skill             : 技能操作统一入口（action=load/run_script/read_file/write/delete/reload，合并自 get_skill_content/run_script/read_asset/write_skill_file/reload_skills）
//   - kv                : 会话 KV 统一入口（action=get/set/append，合并自 kv_get/kv_set/kv_append）
//   - fs                : 文件系统统一入口（action=list/stat/read/write/delete/move/mkdir，合并自 7 个 fs_* 工具）
//   - tool_request      : 工具需求统一入口（action=request/list/close，合并自 3 个工具）
//   - output_file       : 文件输出统一入口（action=write/download，合并自 write_output_file/serve_file_download）
//     write 写文件后自动生成下载 token，一次调用返回 path+download_url
//   - feishu            : 飞书操作统一入口（action=send/webhook/get_config/set_config，合并自 4 个工具）
//   - wecom             : 企业微信统一入口（action=send/get_config/set_config，合并自 3 个工具）
//   - mcp               : MCP 外接能力统一入口（action=list/detail/call，通过 internal/mcp/registry.go 懒加载）
//   - desktop           : 桌面控制统一入口（action=screenshot/get_screen_size/mouse_move/left_click/right_click/double_click/type/key/scroll/drag，需 DESKTOP_ENABLED=true）
//
// 依赖注入策略：
//   - ProgressSender    通过 context.Value 注入，供 send_progress 使用
//   - InteractiveSender 通过 context.Value 注入，供 send_options / send_confirm 使用
//   - sessionID         通过 context.Value 注入，供 kv 工具访问数据库
//     均在 agent.Run() 启动时设置，tool 包不直接依赖 agent/handler 包。
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

// WithUserID 将当前 userID 注入 context，供 get_session_info 工具读取
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDCtxKey{}, userID)
}

func userIDFromCtx(ctx context.Context) string {
	s, _ := ctx.Value(userIDCtxKey{}).(string)
	return s
}

// ========== Context 注入：SubagentSpawner ==========

// SubagentSpawner 后台启动子 agent 的函数类型，由 agent.Run() 注入。
// taskID：sub_tasks 表主键；userID/childSessionID：子会话身份；taskDesc：传给子 agent 的完整任务描述。
type SubagentSpawner func(taskID uint, userID, childSessionID, taskDesc, parentSessionID string)

type subagentSpawnerKey struct{}

// WithSubagentSpawner 将 SubagentSpawner 注入 context
func WithSubagentSpawner(ctx context.Context, s SubagentSpawner) context.Context {
	return context.WithValue(ctx, subagentSpawnerKey{}, s)
}

func subagentSpawnerFromCtx(ctx context.Context) SubagentSpawner {
	s, _ := ctx.Value(subagentSpawnerKey{}).(SubagentSpawner)
	return s
}

// ========== Context 注入：SubtaskCanceler ==========

// SubtaskCanceler 主动取消子 agent 任务的函数类型，由 agent.Run() 注入。
// taskID：sub_tasks 表主键；返回 true 表示找到了取消函数并已调用。
type SubtaskCanceler func(taskID uint) bool

type subtaskCancelerKey struct{}

// WithSubtaskCanceler 将 SubtaskCanceler 注入 context
func WithSubtaskCanceler(ctx context.Context, c SubtaskCanceler) context.Context {
	return context.WithValue(ctx, subtaskCancelerKey{}, c)
}

func subtaskCancelerFromCtx(ctx context.Context) SubtaskCanceler {
	c, _ := ctx.Value(subtaskCancelerKey{}).(SubtaskCanceler)
	return c
}

// ========== Context 注入：TaskID（子 agent 自身 task ID）==========

type taskIDCtxKey struct{}

// WithTaskID 将当前 goroutine 对应的 sub_task ID 注入 context，仅在 RunBackground 中设置。
func WithTaskID(ctx context.Context, taskID uint) context.Context {
	return context.WithValue(ctx, taskIDCtxKey{}, taskID)
}

func taskIDFromCtx(ctx context.Context) uint {
	id, _ := ctx.Value(taskIDCtxKey{}).(uint)
	return id
}

// ========== Context 注入：SubtaskProgressReporter ==========

// SubtaskProgressReporter 子 agent 主动上报进度的函数类型，由 RunBackground 注入。
// 调用后更新 DB 中的 progress_summary，并按 notify_policy 决定是否通知父会话。
type SubtaskProgressReporter func(progress string)

type subtaskProgressReporterKey struct{}

// WithSubtaskProgressReporter 将 SubtaskProgressReporter 注入 context
func WithSubtaskProgressReporter(ctx context.Context, r SubtaskProgressReporter) context.Context {
	return context.WithValue(ctx, subtaskProgressReporterKey{}, r)
}

func subtaskProgressReporterFromCtx(ctx context.Context) SubtaskProgressReporter {
	r, _ := ctx.Value(subtaskProgressReporterKey{}).(SubtaskProgressReporter)
	return r
}

// ========== Context 注入：ParentNotifier ==========

// ParentNotifier 子 agent 向父 session 注入中间消息并触发新 LLM turn 的函数类型。
// 由 RunBackground 注入；调用后异步触发父会话的 notifyMidTask，不阻塞子 agent 当前执行。
type ParentNotifier func(message string)

type parentNotifierKey struct{}

// WithParentNotifier 将 ParentNotifier 注入 context
func WithParentNotifier(ctx context.Context, n ParentNotifier) context.Context {
	return context.WithValue(ctx, parentNotifierKey{}, n)
}

func parentNotifierFromCtx(ctx context.Context) ParentNotifier {
	n, _ := ctx.Value(parentNotifierKey{}).(ParentNotifier)
	return n
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
	e.register("notify", handleNotify) // 合并自 send_progress / send_options / send_confirm
	e.register("skill", e.handleSkill) // 合并自 get_skill_content / run_script / read_asset / write_skill_file / reload_skills
	e.register("kv", handleKv)         // 合并自 kv_get / kv_set / kv_append
	e.register("update_role_md", handleUpdateRoleMD)
	e.register("get_session_info", handleGetSessionInfo)
	e.register("read_file", handleReadFile) // 合并自 read_file / read_pdf / read_image（按扩展名路由）
	e.register("fs", handleFs)              // 合并自 fs_list / fs_stat / fs_read / fs_write / fs_delete / fs_move / fs_mkdir
	e.register("tool_request", e.handleToolRequest) // 合并自 request_tool / list_tool_requests / close_tool_request
	e.register("output_file", handleOutputFile)     // 合并自 write_output_file / serve_file_download
	e.register("exec", handleExec)
	e.register("exec_run", handleExecRun)
	e.register("process", handleProcess)
	e.register("feishu", handleFeishu) // 合并自 feishu_send / feishu_webhook / get_feishu_config / set_feishu_config
	e.register("wecom", handleWecom)   // 合并自 wecom_send / get_wecom_config / set_wecom_config
	e.register("weixin", handleWeixin) // 微信个人号：bind / status / unbind / send
	e.register("browser", handleBrowser)
	e.register("web_fetch", handleWebFetch)
	e.register("code_search", handleCodeSearch)
	e.register("cron", handleCron)
	e.register("nano_banana", handleNanoBanana)
	e.register("get_tool_doc", handleGetToolDoc)
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
	e.register("spawn_subagent", handleSpawnSubagent)
	e.register("cancel_subtask", handleCancelSubtask)
	e.register("report_task_progress", handleReportTaskProgress)
	e.register("notify_parent", handleNotifyParent)
	if config.Cfg.DesktopEnabled {
		e.register("desktop", handleDesktop)
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
	result, err := h(ctx, argsJSON)
	// 工具调用成功但无输出时返回 {"ok":true}，防止 LLM 因空工具结果误判任务已结束退出循环。
	// 纯 JSON 格式，LLM 不会将其当作对话文本输出。
	// exec 已在 execDoneResult 中单独处理（含 exit code），此处作为其余工具的统一兜底。
	if err == nil && strings.TrimSpace(result) == "" {
		result = `{"ok":true}`
	}
	return result, err
}

// ToolDefinitions 返回所有工具的 LLM function calling 定义。
// initialized=true 后过滤掉仅在 bootstrap 阶段使用的工具，减少 token 消耗。
func (e *Executor) ToolDefinitions() []llm.Tool {
	all := []llm.Tool{
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "notify",
				Description: "UI notification. action: progress (push message, no wait) / options (show choice buttons, STOP and wait for reply) / confirm (show confirm dialog, STOP and wait for reply) / upload (show file upload widget, STOP and wait for reply; next turn contains uploaded path or \"skip\").",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":  map[string]any{"type": "string", "enum": []string{"progress", "options", "confirm", "upload"}},
						"message": map[string]any{"type": "string", "description": "progress: text; confirm: action description"},
						"title":   map[string]any{"type": "string", "description": "options: title above buttons; upload: widget title"},
						"prompt":  map[string]any{"type": "string", "description": "upload: additional description shown to user"},
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
				Description: "Skill operations. action: load (read full SKILL.md; required before running) / run_script / read_file / write (SKILL.md or sub_path file) / delete / reload / done (mark skill execution complete, call after delivering final output). Call get_tool_doc(\"skill\") for details.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":         map[string]any{"type": "string", "enum": []string{"load", "run_script", "read_file", "write", "delete", "reload", "done"}},
						"skill_id":       map[string]any{"type": "string"},
						"script_name":    map[string]any{"type": "string"},
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
				Description: "Session scratch space — lost when session ends. For inter-step data within one session only. For data that must outlive the session use memory(target=user_kv). action: get (null if missing) / set (overwrite, any JSON) / append (add to array).",
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
				Name:        "get_session_info",
				Description: "Return current context: user_id, session_id, session_source (web/feishu), session_title, and parent_session_id (if in a continuation session).",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "read_file",
				Description: "Read any uploaded file. .docx/.pptx/.xlsx → text; .pdf → page-by-page text (pages=\"1-5\" to select range, render=true for scanned docs); .jpg/.png/.gif/.webp → visual analysis.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":   map[string]any{"type": "string", "description": "File path (uploads/, output/, or /tmp)"},
						"pages":  map[string]any{"type": "string", "description": "PDF only: page range e.g. \"1-5\" or \"1,3,7-10\" (omit for all pages)"},
						"render": map[string]any{"type": "boolean", "description": "PDF only: render pages as images for scanned documents"},
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
				Description: "File system ops inside sandbox. action: list / stat / read (images→multimodal; text max 512KB) / write / delete / move / mkdir. Call get_tool_doc(\"fs\") for path restrictions and parameter details.",
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
				Description: "Execute a shell command (bash -c). Use code_search for reading/searching source files; use code_search(action=git) for simple git lookups. Append \"; echo '[cmd:exit=$?]'\" to commands that produce no output. Call get_tool_doc(\"exec\") for advanced params.",
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
					"properties":           map[string]any{},
					"additionalProperties": true,
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
						"extra_fs_dirs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Extra absolute paths all users can read and write via fs (shared directories)"},
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
					"properties":           map[string]any{},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "wecom",
				Description: "WeCom operations. action: send (Webhook push, supports text/markdown/image/file) / get_config / set_config / set_bot_config / get_bot_config.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":      map[string]any{"type": "string", "enum": []string{"send", "get_config", "set_config", "set_bot_config", "get_bot_config"}},
						"text":        map[string]any{"type": "string", "description": "send: text or markdown content"},
						"msgtype":     map[string]any{"type": "string", "enum": []string{"text", "markdown"}, "description": "send: default text"},
						"file_path":   map[string]any{"type": "string", "description": "send: local file path — image (jpg/png/gif/webp, ≤2MB) sent as base64; other types uploaded via media API"},
						"webhook_url": map[string]any{"type": "string", "description": "send: optional (uses stored if omitted); set_config: required"},
						"bot_id":      map[string]any{"type": "string", "description": "set_bot_config: WeCom AI bot ID (format: botid_...)"},
						"secret":      map[string]any{"type": "string", "description": "set_bot_config: WeCom AI bot secret (stored encrypted)"},
					},
					"required": []string{"action"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "weixin",
				Description: "微信个人号绑定与消息收发（支持文本、图片、文件）。action: bind/status/unbind/send。【发消息工作流】：① 先调 status 检查是否已绑定且在线；② 若未绑定(bound=false)先调 bind 发起扫码；③ 已绑定后调 send，to 填目标微信 ID。status 返回的 owner_weixin_id 是用户自己绑定的微信账号 ID，发给自己时直接用它。【收消息】图片/文件会自动下载，路径以 [文件: /path] 附在消息里，可用 read_file 读取。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{"type": "string", "enum": []string{"bind", "status", "unbind", "send"}},
						"to":     map[string]any{"type": "string", "description": "send 必填：目标微信用户 ID。发给用户自己时填 status 返回的 owner_weixin_id"},
						"text":   map[string]any{"type": "string", "description": "send：文字消息内容（与 file 二选一或同时使用）"},
						"file":   map[string]any{"type": "string", "description": "send：发送图片或文件，填本地路径（/uploads/... 或绝对路径）或公开 URL；扩展名为 jpg/png/gif/webp 等会作为图片发送，其他作为文件发送"},
					},
					"required": []string{"action"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "browser",
				Description: "Headless Chromium automation. Use snapshot to read page content (never screenshot for that). Call get_tool_doc(\"browser\") before use for full params and the login-handling protocol.",
				Parameters: map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "web_fetch",
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
				Name:        "code_search",
				Description: "Explore codebase & docs: tree / glob / grep / outline / chunk_read / git / ast_grep / comby. Call get_tool_doc(\"code_search\") for full parameter docs.",
				Parameters: map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": true,
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "cron",
				Description: "Manage scheduled tasks (add/list/update/remove/run/cancel/status/history). cancel: send cancellation signal to a currently-running job (id required); status returns running_jobs list. Use history to query recent run records (status, duration, errors). The schedule field is complex — call get_tool_doc(\"cron\") first for full parameter docs.",
				Parameters: map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": true,
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
					"properties":           map[string]any{},
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
				Name:        "session_search",
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
	if config.Cfg.DesktopEnabled {
		all = append(all, llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "desktop",
				Description: "Control the desktop: take screenshots, move mouse, click, type text, press keys, scroll, drag. action=screenshot returns current screen as an image the LLM can see. Recommended workflow: screenshot → analyze → act → screenshot to verify.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type": "string",
							"enum": []string{"screenshot", "get_screen_size", "mouse_move", "left_click", "right_click", "double_click", "type", "key", "scroll", "drag"},
						},
						"x":         map[string]any{"type": "integer", "description": "X coordinate (pixels)"},
						"y":         map[string]any{"type": "integer", "description": "Y coordinate (pixels)"},
						"text":      map[string]any{"type": "string", "description": "Text to type (action=type)"},
						"key":       map[string]any{"type": "string", "description": "Key combo, e.g. ctrl+c, Return, escape, ctrl+alt+t (action=key)"},
						"direction": map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}, "description": "Scroll direction (action=scroll)"},
						"amount":    map[string]any{"type": "integer", "description": "Scroll amount in lines, default 3 (action=scroll)"},
						"start_x":   map[string]any{"type": "integer", "description": "Drag start X (action=drag)"},
						"start_y":   map[string]any{"type": "integer", "description": "Drag start Y (action=drag)"},
						"end_x":     map[string]any{"type": "integer", "description": "Drag end X (action=drag)"},
						"end_y":     map[string]any{"type": "integer", "description": "Drag end Y (action=drag)"},
					},
					"required": []string{"action"},
				},
			},
		})
	}
	// 子 agent 工具
	all = append(all,
		llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "spawn_subagent",
				Description: "Delegate a subtask to an independent background subagent. Returns task_id immediately; subagent runs async. Use for long-running, parallelizable, or context-isolating work.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"task": map[string]any{
							"type":        "string",
							"description": "Full task description for the subagent — be specific and self-contained",
						},
						"label": map[string]any{
							"type":        "string",
							"description": "Short human-readable task label shown in notifications and logs",
						},
						"context": map[string]any{
							"type":        "string",
							"description": "Background information appended to the subagent's prompt",
						},
						"notify_policy": map[string]any{
							"type":        "string",
							"description": "When to notify the parent session. done_only (default): terminal state only; state_changes: on running + terminal; silent: never notify.",
							"enum":        []string{"done_only", "state_changes", "silent"},
						},
						"retain_hours": map[string]any{
							"type":        "integer",
							"description": "Hours to retain the task record after it reaches a terminal state. 0 = use global default (~72 h).",
						},
					},
					"required": []string{"task"},
				},
			},
		},
		llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "cancel_subtask",
				Description: "Cancel a running subagent task. force=false (default): graceful signal, stops after current call → cancelled; force=true: immediate DB kill → killed. Only queued/running tasks can be cancelled.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"task_id": map[string]any{
							"type":        "integer",
							"description": "task_id returned by spawn_subagent",
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "Cancellation reason (recorded in error_msg)",
						},
						"force": map[string]any{
							"type":        "boolean",
							"description": "true = force-kill to killed immediately; false (default) = graceful cancel to cancelled",
						},
					},
					"required": []string{"task_id"},
				},
			},
		},
		llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "report_task_progress",
				Description: "[Subagent only] Report current task progress to the parent session. Updates DB and shows in the subagent card. Call after each major step.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"progress": map[string]any{
							"type":        "string",
							"description": "Short description of current progress, e.g. 'Data collection complete, starting analysis'",
						},
					},
					"required": []string{"progress"},
				},
			},
		},
		llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "notify_parent",
				Description: "[Subagent only] Inject a message into the parent session and immediately trigger a new parent LLM turn. Unlike report_task_progress (DB-only), this wakes the parent agent for real-time decisions. Returns immediately without blocking the subagent.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"message": map[string]any{
							"type":        "string",
							"description": "Message to inject into the parent session, e.g. 'Phase 1 done: 200 records collected, 3 anomalies found — recommend deciding whether to continue'",
						},
					},
					"required": []string{"message"},
				},
			},
		},
	)
	// initialized=true 后，过滤掉仅在 bootstrap 阶段使用的工具
	if cfg, err := storage.GetAppConfig(); err == nil && cfg.Initialized {
		bootstrapOnly := map[string]bool{"update_role_md": true}
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

// handleNotify 通过 action 字段分发，替代 send_progress / send_options / send_confirm / send_file_upload 四个工具。
// action: progress / options / confirm / upload
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
	case "upload":
		return handleSendFileUpload(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown notify action: %q (valid: progress/options/confirm/upload)", base.Action)
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
		return "", fmt.Errorf("parse notify upload args: %w", err)
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

// listDirFiles 列出目录下的直接子文件（非递归）；目录不存在时返回 nil。
func listDirFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
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
	// 构建技能元信息头：物理位置 + 可用文件清单，让 Agent 在执行前就掌握全部上下文。
	if skillDir, ok := skill.Store.GetSkillDir(userID, args.SkillID); ok {
		var hdr strings.Builder
		fmt.Fprintf(&hdr, "[Skill directory: %s]\n", skillDir)
		if scripts := listDirFiles(filepath.Join(skillDir, "script")); len(scripts) > 0 {
			fmt.Fprintf(&hdr, "[Available scripts: %s]\n", strings.Join(scripts, ", "))
		}
		if assets := listDirFiles(filepath.Join(skillDir, "assets")); len(assets) > 0 {
			fmt.Fprintf(&hdr, "[Available assets: %s]\n", strings.Join(assets, ", "))
		}
		if refs := listDirFiles(filepath.Join(skillDir, "references")); len(refs) > 0 {
			fmt.Fprintf(&hdr, "[Available references: %s]\n", strings.Join(refs, ", "))
		}
		content = hdr.String() + "\n" + content

		// Approximate LFU usage tracking: record when a self-improving skill is loaded.
		siMarker := filepath.Join("self-improving", "skills")
		if strings.Contains(skillDir, siMarker) {
			userSkillsDir := skill.Store.GetUserSkillsDir(userID)
			decayHours := config.Cfg.SelfImprovingLFUDecayHours
			go skill.RecordSelfImprovingUse(userSkillsDir, args.SkillID, decayHours)
		}
	}
	// 将当前激活的 skill ID 持久化到 session KV，
	// 供 buildSystemPrompt 在每轮重建时自动注入完整 Skill 内容（防压缩丢失）
	sessionID := sessionIDFromCtx(ctx)
	if sessionID != "" {
		if kv, err := storage.GetSessionKV(sessionID); err == nil {
			kv["_active_skill_id"] = args.SkillID
			_ = storage.UpdateSessionKV(sessionID, kv)
		}
	}
	return content, nil
}

// handleDoneSkill 标记当前 skill 执行完成，清除 session KV 中的激活状态。
// LLM 在 skill 的最后一步（输出最终结果后）调用此 action，
// 避免 skill 内容持续占用系统 prompt 空间。
func handleDoneSkill(ctx context.Context, _ string) (string, error) {
	sessionID := sessionIDFromCtx(ctx)
	if sessionID == "" {
		return "Skill execution completed.", nil
	}
	kv, err := storage.GetSessionKV(sessionID)
	if err != nil {
		return "", fmt.Errorf("get session kv: %w", err)
	}
	delete(kv, "_active_skill_id")
	if err := storage.UpdateSessionKV(sessionID, kv); err != nil {
		return "", fmt.Errorf("clear active skill: %w", err)
	}
	return "Skill execution completed. Active skill protection cleared.", nil
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
		baseDir := skill.Store.GetBaseDir()
		uid := userIDFromCtx(ctx)
		return "", fmt.Errorf("skill %q not found; searched:\n  1. %s\n  2. %s\n  3. %s",
			args.SkillID,
			filepath.Join(baseDir, "users", uid, "self-improving", "skills", args.SkillID),
			filepath.Join(baseDir, "users", uid, args.SkillID),
			filepath.Join(baseDir, "system", args.SkillID))
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
		return "", fmt.Errorf("script %q not found in skill %q (searched: %s)", args.ScriptName, args.SkillID, absScriptPath)
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
	// 工作目录设为项目根目录（skills/ 的父目录），使相对路径 uploads/、output/ 等可直接访问
	cmd.Dir = filepath.Dir(skill.Store.GetBaseDir())
	// 注入 session / user / skill 上下文
	cmd.Env = append(os.Environ(),
		"SKILL_SESSION_ID="+sessionIDFromCtx(ctx),
		"SKILL_USER_ID="+userIDFromCtx(ctx),
		"SKILL_DIR="+skillDir, // 技能根目录，脚本如需访问自身目录下的文件可用此变量
	)

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

// handleReadSkillFile 读取技能根目录下任意子路径的文件内容，与 handleWriteSkillFile 对称。
//
// 参数：
//   - skill_id: 技能唯一标识
//   - sub_path:  相对于技能根目录的路径，例如 "extras/config.yaml"、"data/schema.json"
//     （如需读取 SKILL.md 本身，使用 action=load；script/assets/references 有各自专用 action）
//
// 安全约束：路径不得穿越出技能根目录
func handleReadSkillFile(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SkillID string `json:"skill_id"`
		SubPath string `json:"sub_path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse read_file args: %w", err)
	}
	if args.SkillID == "" {
		return "", fmt.Errorf("skill_id is required")
	}
	if args.SubPath == "" {
		return "", fmt.Errorf("sub_path is required")
	}

	skillDir, ok := skill.Store.GetSkillDir(userIDFromCtx(ctx), args.SkillID)
	if !ok {
		baseDir := skill.Store.GetBaseDir()
		uid := userIDFromCtx(ctx)
		return "", fmt.Errorf("skill %q not found; searched:\n  1. %s\n  2. %s\n  3. %s",
			args.SkillID,
			filepath.Join(baseDir, "users", uid, "self-improving", "skills", args.SkillID),
			filepath.Join(baseDir, "users", uid, args.SkillID),
			filepath.Join(baseDir, "system", args.SkillID))
	}

	absSkillDir, err := filepath.Abs(skillDir)
	if err != nil {
		return "", fmt.Errorf("resolve skill dir: %w", err)
	}

	targetPath := filepath.Clean(filepath.Join(absSkillDir, args.SubPath))
	absTargetPath, err := filepath.Abs(targetPath)
	if err != nil {
		return "", fmt.Errorf("resolve target path: %w", err)
	}
	if !strings.HasPrefix(absTargetPath, absSkillDir+string(filepath.Separator)) {
		return "", fmt.Errorf("sub_path escapes skill directory")
	}

	data, err := os.ReadFile(absTargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file %q not found in skill %q (searched: %s)", args.SubPath, args.SkillID, absTargetPath)
		}
		return "", fmt.Errorf("read file %q: %w", args.SubPath, err)
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
//   - 省略 → 写入 SKILL.md（默认行为）
//   - "script/foo.sh"         → 写入 script/ 目录
//   - "assets/bar.json"       → 写入 assets/ 目录
//   - "references/baz.md"     → 写入 references/ 目录
//   - "extras/config.yaml"    → 写入任意自定义子目录（如导入第三方技能包时的原始结构）
//
// 安全约束：
//   - skill_id 只允许小写字母、数字、下划线（防止路径注入）
//   - sub_path 不得穿越出技能根目录（路径穿越检查）
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
			return "", fmt.Errorf("SKILL.md 格式错误：%w\n提示：请先调用 skill(action=read_file, skill_id=skill_creator, sub_path=\"assets/skill_template.md\") 获取标准格式模板，再重新写入", err)
		}
		if parsed.SkillID != args.SkillID {
			return "", fmt.Errorf("内容中的 skill_id=%q 与参数 skill_id=%q 不一致，请保持一致", parsed.SkillID, args.SkillID)
		}

		if err := skill.ScanSkillFile(args.Content, ""); err != nil {
			return "", fmt.Errorf("skill security scan rejected SKILL.md: %w", err)
		}
		mdPath := filepath.Join(absSkillDir, "SKILL.md")
		_, isUpdate := os.Stat(mdPath)
		if err := os.MkdirAll(absSkillDir, 0o755); err != nil {
			return "", fmt.Errorf("create skill directory: %w", err)
		}
		if err := os.WriteFile(mdPath, []byte(args.Content), 0o644); err != nil {
			return "", fmt.Errorf("write SKILL.md: %w", err)
		}
		if isUpdate == nil {
			return fmt.Sprintf("skill %q updated (SKILL.md overwritten)", args.SkillID), nil
		}
		return fmt.Sprintf("SKILL.md written to %s", mdPath), nil
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

// handleDeleteSkill 删除用户自有技能目录（及其所有子文件）并热更新 Store。
//
// 允许删除的路径（两处均会检查，存在的全部删除）：
//   - skills/users/{userid}/{skillid}/                        （用户手工技能）
//   - skills/users/{userid}/self-improving/skills/{skillid}/  （自进化技能）
//
// 安全约束：
//   - skill_id 只允许小写字母、数字、下划线，防止路径注入
//   - 严格校验最终路径在允许前缀内，防止路径穿越
//   - skills/system/ 始终只读，无论如何不允许删除
func (e *Executor) handleDeleteSkill(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SkillID string `json:"skill_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse delete args: %w", err)
	}
	if args.SkillID == "" {
		return "", fmt.Errorf("skill_id is required")
	}
	if !skillIDRegex.MatchString(args.SkillID) {
		return "", fmt.Errorf("skill_id %q is invalid: only lowercase letters, digits, and underscores are allowed", args.SkillID)
	}

	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}

	baseDir := skill.Store.GetBaseDir()
	if baseDir == "" {
		return "", fmt.Errorf("skills directory not initialized")
	}

	// 两个允许的候选根目录
	absUserBase, err := filepath.Abs(filepath.Join(baseDir, "users", userID))
	if err != nil {
		return "", fmt.Errorf("resolve user skills dir: %w", err)
	}
	absSIBase, err := filepath.Abs(filepath.Join(baseDir, "users", userID, "self-improving", "skills"))
	if err != nil {
		return "", fmt.Errorf("resolve self-improving skills dir: %w", err)
	}

	candidateUser := filepath.Clean(filepath.Join(absUserBase, args.SkillID))
	candidateSI := filepath.Clean(filepath.Join(absSIBase, args.SkillID))

	// 路径穿越校验
	if !strings.HasPrefix(candidateUser, absUserBase+string(filepath.Separator)) {
		return "", fmt.Errorf("skill_id escapes user skills directory")
	}
	if !strings.HasPrefix(candidateSI, absSIBase+string(filepath.Separator)) {
		return "", fmt.Errorf("skill_id escapes self-improving skills directory")
	}

	// 额外防护：user 路径不得落入 self-improving 子目录（skill_id 含连字符时理论上不可能，但防御性保留）
	siMarker := filepath.Join(absUserBase, "self-improving")
	if strings.HasPrefix(candidateUser, siMarker+string(filepath.Separator)) || candidateUser == siMarker {
		return "", fmt.Errorf("cannot delete via user path when target is inside self-improving; skill_id resolved to a self-improving subdirectory")
	}

	var deleted []string
	for _, candidate := range []string{candidateUser, candidateSI} {
		if _, statErr := os.Stat(candidate); os.IsNotExist(statErr) {
			continue
		}
		if removeErr := os.RemoveAll(candidate); removeErr != nil {
			return "", fmt.Errorf("delete skill directory %q: %w", candidate, removeErr)
		}
		deleted = append(deleted, candidate)
	}

	if len(deleted) == 0 {
		return "", fmt.Errorf("skill %q not found for user %q (searched:\n  1. %s\n  2. %s)",
			args.SkillID, userID, candidateUser, candidateSI)
	}

	if reloadErr := skill.Store.Reload(); reloadErr != nil {
		return "", fmt.Errorf("skill deleted but reload failed: %w", reloadErr)
	}
	if syncErr := e.SyncToolRequests(); syncErr != nil {
		return "", fmt.Errorf("skill deleted but sync tool requests failed: %w", syncErr)
	}

	return fmt.Sprintf("skill %q deleted (%d location(s) removed) and store reloaded", args.SkillID, len(deleted)), nil
}

// handleSkill 通过 action 字段分发，替代 5 个独立工具：
// get_skill_content / run_script / read_asset / read_reference / write_skill_file / reload_skills
// action: load / run_script / read_file / write / delete / reload
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
	case "read_file":
		return handleReadSkillFile(ctx, argsJSON)
	case "write":
		return handleWriteSkillFile(ctx, argsJSON)
	case "delete":
		return e.handleDeleteSkill(ctx, argsJSON)
	case "reload":
		return e.handleReloadSkills(ctx, argsJSON)
	case "done":
		return handleDoneSkill(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown skill action: %q (valid: load/run_script/read_file/write/delete/reload/done)", base.Action)
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
		return "", fmt.Errorf("系统已完成初始化，无法通过工具修改（如需重置，请联系管理员手动修改）")
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
		return "", fmt.Errorf("更新系统角色信息失败: %w", err)
	}
	// 仅在 bootstrap 最后一步（finalize=true）时锁定初始化入口，确保所有系统技能均已创建完毕
	if args.Finalize {
		_ = storage.MarkInitialized()
	}
	return "系统角色信息已写入并热加载，新角色从下一轮对话起正式生效", nil
}

// handleGetSessionInfo 返回当前会话的完整上下文信息：
// user_id、session_id、session_source、session_title、parent_session_id（非空时）。
func handleGetSessionInfo(ctx context.Context, _ string) (string, error) {
	uid := userIDFromCtx(ctx)
	if uid == "" {
		return "", fmt.Errorf("user_id not available in context")
	}
	sid := sessionIDFromCtx(ctx)

	result := map[string]any{
		"user_id":    uid,
		"session_id": sid,
	}

	// 从 DB 补充 Session 维度的信息；失败时降级，不阻塞工具调用
	if sid != "" {
		if sess, err := storage.GetSession(sid); err == nil && sess != nil {
			result["session_source"] = sess.Source
			if sess.Title != "" {
				result["session_title"] = sess.Title
			}
			if sess.ParentSessionID != "" {
				result["parent_session_id"] = sess.ParentSessionID
			}
		}
	}

	b, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(b), nil
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

// handleReadFile 统一文件读取入口（合并自 read_file / read_pdf / read_image）。
// 根据扩展名自动路由：
//   - .jpg/.jpeg/.png/.gif/.webp/.bmp → 多模态图片分析（handleReadImage）
//   - .pdf                            → 分页 PDF 提取（handleReadPDF，支持 pages / render）
//   - .docx/.pptx/.xlsx/.doc         → Office 文本提取（readUploadedFile）
func handleReadFile(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse read_file args: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("path is required")
	}

	switch strings.ToLower(filepath.Ext(args.Path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return handleReadImage(ctx, argsJSON)
	case ".pdf":
		return handleReadPDF(ctx, argsJSON)
	default:
		text, err := readUploadedFile(args.Path)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "(文件中未提取到文本内容，可能是扫描件或加密文档)", nil
		}
		return text, nil
	}
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
