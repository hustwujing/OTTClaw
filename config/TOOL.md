# Available Tools

## exec

**Purpose**: Execute a shell command via `bash -c`. Short commands return output directly; long-running commands background automatically and return a `session_id` — use `process(action=poll)` to stream output.

**Basic parameters** (always available):
- `command` (string, required): Shell command string
- `workdir` (string, optional): Working directory; defaults to server's working directory

**Advanced parameters** (rarely needed — pass only when required):
- `env` (object, optional): Additional environment variables, e.g. `{"FOO": "bar"}`
- `timeout_sec` (integer, optional): Hard timeout in seconds; default 1800
- `yield_ms` (integer, optional): Milliseconds to wait before backgrounding; default 10000 (10 s). If the command finishes within this window, output is returned inline; otherwise a `session_id` is returned
- `background` (boolean, optional): `true` to skip the wait window and immediately background the command; use for commands known to be long-running (e.g. `npm run dev`)

**Return values**:
- Inline finish: `{ exit_code, stdout, stderr }`
- Backgrounded: `{ session_id, output_so_far }` — follow up with `process(action=poll, session_id=...)`

**Examples**:
```json
// Simple command
{"command": "ls -la /tmp"}

// Run in specific directory
{"command": "go build ./...", "workdir": "/app"}

// Background a dev server immediately
{"command": "npm run dev", "workdir": "/app", "background": true}

// Pass extra env vars
{"command": "python3 train.py", "env": {"CUDA_VISIBLE_DEVICES": "0"}, "timeout_sec": 7200}
```

---

## process

**Purpose**: Process control panel for uniformly managing all exec sessions (list, poll output, interact, terminate, clean up).

**Action overview**:

| action | Description |
|--------|------|
| `list` | List all sessions (id, command, status, elapsed time) |
| `poll` | Wait up to timeout ms for new incremental output; returns `{status, new_output, elapsed_sec}` |
| `log` | View full output history, supports offset/limit pagination (default tail 200 lines) |
| `write` | Write raw text to stdin (no automatic newline) |
| `submit` | Write text to stdin and append Enter (`\r`) |
| `send-keys` | Send named keys (`ctrl-c`, `ctrl-d`, `enter`, `tab`, `escape`, `up`, `down`, `f1`…`f12`, etc.) |
| `paste` | Paste multi-line text using the bracketed paste protocol |
| `kill` | Send a signal to the process group (default `SIGTERM`; options: `SIGKILL`, `SIGINT`, `SIGHUP`) |
| `clear` | Clear the incremental buffer (does not affect full history) |
| `remove` | Remove a session from the registry (kills first if running) |

**Parameters**:
- `action` (string, required): The action name from the table above
- `session_id` (string): Required for all actions except `list`
- `timeout` (integer, optional): Maximum wait time in milliseconds for `poll`; default 5000, max 30000
- `offset` (integer, optional): Starting line for `log` (0-indexed; negative counts from the end)
- `limit` (integer, optional): Maximum number of lines to return for `log`; default 200
- `text` (string): Text content for `write`/`submit`/`paste`
- `key` (string): Key name for `send-keys`
- `signal` (string, optional): Signal name for `kill`; default `SIGTERM`

**Typical usage (polling a long-running command)**:
```
exec_run(pending_id)
  → {status:"running", session_id:"es_xxx", output_so_far:"..."}
process(action="poll", session_id="es_xxx", timeout=10000)
  → {status:"running", new_output:"..."} or {status:"done", exit_code:0, output:"..."}
```

Sessions expire and are automatically cleaned up after 2 hours.

---

## feishu

**Purpose**: Unified Feishu operations. action: `send` / `webhook` / `get_config` / `set_config`.

**action: "send"** — Send via Feishu Bot API
- `receive_id` (string, required): Recipient ID, or `"self"` to use the user's bound open_id
- `receive_id_type` (string, optional): `open_id` (default) / `user_id` / `chat_id` / `union_id`; auto-inferred from prefix when `"self"` is used
- `text` (string): Text content (required unless `file_path` is provided)
- `file_path` (string): Local file path; images auto-detected and uploaded

**action: "webhook"** — Send to a Feishu group via Webhook URL (no Bot credentials required)
- `webhook_url` (string, required): Feishu group custom bot Webhook URL
- `text` (string, required): Text content

**action: "get_config"** — Read the current user's Feishu config (App ID, Webhook URL; AppSecret masked)
- No parameters required

**action: "set_config"** — Write or update Feishu bot configuration. **Must obtain user confirmation via `notify(action=confirm)` before calling.**
- `app_id` (string, optional): Feishu App ID
- `app_secret` (string, optional): Plaintext App Secret (encrypted before storage)
- `webhook_url` (string, optional): Group bot Webhook URL
- `self_open_id` (string, optional): The user's own Feishu ID; once set, `feishu(action=send, receive_id="self")` resolves automatically

---

## browser

Headless Chromium automation. Workflow: `launch` → `navigate` → `snapshot` → act (click/type/…) → `close`.

**snapshot vs screenshot**: `snapshot` = aria tree for reading/interacting (zero image tokens, use always). `screenshot` = visual image for user only — never use to "see" the page yourself.

**Actions**: `launch`, `close`, `navigate`(url), `snapshot`, `screenshot`(fullPage?), `render`(html,selector?,waitSelector?,timeoutMs?), `click`(ref), `type`(ref,text), `select`(ref,values), `hover`(ref), `scroll`(ref|deltaY), `press_key`(key,ref?), `wait`(selector|timeoutMs), `evaluate`(script), `tabs`, `tab_open`(url?), `tab_close`(targetIdx), `save_cookies`(cookieName), `load_cookies`(cookieName), `list_cookies`

**render**: One-step HTML rendering + screenshot. Pass full HTML string via `html`, optional `selector` (element to capture), `waitSelector` (element to wait for before screenshot, defaults to `selector + ' svg'`), `timeoutMs`. Auto-launches browser if needed. Ideal for Mermaid diagrams — replaces the multi-step write→launch→navigate→wait→screenshot→close flow.

**Key params**: `url`, `ref` (from snapshot, e.g. `e3`), `text`, `key`, `values`, `selector`, `script`, `html`, `deltaY`(default 500), `fullPage`, `targetIdx`, `cookieName`(letters/digits/-/_ only), `timeoutMs`(default 10000), `visible`(bool, `launch` only — see login flow), `waitSelector`

**Returns**: snapshot→`{url,title,snapshot,refCount}` · screenshot→sent to user automatically, do NOT embed URL · navigate→`{url,title,httpStatus}` · others→`{status,url?}`

**Notes**: snapshot/click/navigate clears refs — call snapshot again after. Contexts are per-user isolated. Auto-released after 15 min idle.

**Login / verification** — if login or 2FA detected after navigate:

1. Do NOT screenshot. Call `notify(options)` asking user: `"Open visible browser (local server)"` / `"Guide me manually (remote)"`.
2. **Local**: `close` → `launch(visible=true)` → navigate to login URL → tell user to log in and reply "continue" (or close browser themselves) → **wait** → `close` (safe if already closed) → `launch` (headless) → navigate to original URL → resume task. ⚠️ Visible browser is for login ONLY — do all task work in headless after relaunch. Cookies auto-preserved, no save_cookies needed.
3. **Remote**: `screenshot` → instruct user → wait → continue.

Slider CAPTCHA: ask user for session cookie.

---

## code_search

**Purpose**: Efficiently explore a codebase, providing two operations: recursive directory tree and keyword/regex search. An LLM can grasp project structure or locate target code in 1-2 calls, replacing the inefficient pattern of repeatedly calling `fs_list` + `fs_read`.

**Parameters**:
- `action` (string, required): `"tree"` or `"grep"`
- `path` (string, required): Target directory path

**action: "tree"** — Recursively list the directory tree
- `max_depth` (int, optional): Maximum recursion depth; default 5
- `include` (string, optional): Filename glob filter, e.g. `"*.go"` (matches filenames only)

Returns the directory tree in indented text format, e.g.:
```
my-project/
  main.go
  internal/
    tool/
      executor.go
```
Automatically skips noise directories: `.git`, `node_modules`, `vendor`, `__pycache__`, `.venv`, `.idea`, `.vscode`, `bin`, `dist`, `build`, `.next`. Returns at most 500 entries; truncated with a notice if exceeded.

**action: "grep"** — Recursively search file content
- `pattern` (string, required): Search keyword or regular expression
- `include` (string, optional): Filename glob filter, e.g. `"*.go"`
- `max_results` (int, optional): Maximum number of matches to return; default 50
- `context_lines` (int, optional): Number of context lines to show before and after each match; default 2

Return format (`>` marks the matching line; others are context):
```
> internal/tool/executor.go:194: e.register("code_search", handleCodeSearch)
  internal/tool/executor.go:195: return e
---
  internal/agent/agent.go:186: // Start agent
> internal/agent/agent.go:187: maxIterations := config.Cfg.AgentMaxIterations
  internal/agent/agent.go:188: for i := 0; i < maxIterations; i++ {
```
Automatically skips binary files (NUL byte detection), noise directories (same as tree), and files larger than 1 MB.

**Typical usage**:
```
// Understand project structure
code_search(action="tree", path="internal/")

// Search for tool registration points
code_search(action="grep", path="internal/", pattern="e\\.register\\(", include="*.go")

// Find a function definition
code_search(action="grep", path=".", pattern="func handleExec", include="*.go")
```

---

## cron

**Purpose**: Create and manage scheduled tasks. When a task is due, the system automatically creates an independent session in the background and runs the agent, sending `message` as the user message.

**Action list**:

| action | Required parameters | Description |
|--------|----------|------|
| `status` | — | Returns scheduler running status |
| `list` | — | Lists all scheduled tasks for the current user |
| `add` | `name`, `schedule`, `message` | Create a new task |
| `update` | `id` | Modify a task (optionally update name/schedule/message/enabled) |
| `remove` | `id` | Delete a task |
| `run` | `id` | Trigger once immediately (runs in background, does not wait for result) |

**Three schedule formats**:
- `{"kind":"cron","expr":"0 9 * * *","tz":"Asia/Shanghai"}` — Standard 5-field cron expression
- `{"kind":"every","everyMs":3600000}` — Fixed interval (milliseconds); 1 hour = 3600000
- `{"kind":"at","at":"2026-03-20T09:00:00+08:00"}` — Single absolute time (RFC3339); automatically deleted after triggering

**Important: Time calculation for `at` type**:
- Calculate the target time based on the current time provided in `# Current Time` in the system prompt
- User says "N minutes from now" = current time + N minutes
- **Must use RFC3339 format with timezone offset** (e.g. `+08:00` for Beijing time (CST)); do not use `Z` (UTC) unless the user explicitly requests UTC
- Example: Beijing time (CST) 20:03 + 1 minute = `2026-03-13T20:04:00+08:00`

**Examples**:
```json
// Daily reminder at 9 AM (Beijing time (CST))
{"action":"add","name":"Morning Reminder","schedule":{"kind":"cron","expr":"0 9 * * *","tz":"Asia/Shanghai"},"message":"Please remind me of today's to-do items"}

// Hourly reminder to drink water
{"action":"add","name":"Water Reminder","schedule":{"kind":"every","everyMs":3600000},"message":"Remind me to drink water and give me a health tip"}

// Single execution at Beijing time (CST) 2026-03-20 09:00
{"action":"add","name":"Meeting Reminder","schedule":{"kind":"at","at":"2026-03-20T09:00:00+08:00"},"message":"Remind me to attend today's product review meeting"}
```

---

## nano_banana

**Purpose**: Call the nano-banana-pro model to generate images, supporting three modes: text-to-image, image-to-image, and image editing. Generated results are automatically saved to the server and returned as image markdown that can be directly embedded in the conversation.

**Three modes**:

| action | Description | image_urls |
|--------|------|-----------|
| `txt2img` | Text-to-image: generate an image from a text description | Not required |
| `img2img` | Image-to-image: regenerate based on a reference image | Required |
| `edit` | Image editing: modify the original image according to prompt instructions | Required |

**Parameters**:
- `action` (string, optional): `txt2img` (default) | `img2img` | `edit`
- `prompt` (string, required): Image description or editing instruction
- `image_urls` (array, required for img2img/edit): List of reference images; supports HTTP/HTTPS URLs or server-local paths (e.g. `uploads/3/photo.jpg`)
- `aspect_ratio` (string, optional): Aspect ratio, e.g. `16:9`, `9:16`, `1:1`, `4:3`; default `16:9`
- `size` (string, optional): Resolution tier `2K` (default) or `4K`

**Return value**:
- `path`: Absolute path of the image on the server
- `web_url`: Relative path of the image (can be used with `output_file(action=download)` to generate a download link)
- `inline_image`: Markdown in the format `![Generated Image](/output/...)`, which can be pasted directly into a reply for the user to see the image

**Usage**: After receiving `inline_image`, paste it directly into the reply text:
```
Sure, here is the generated image:

![Generated Image](/output/A/nb_1234567890.jpg)
```

**Examples**:
```json
// Text-to-image
{"action": "txt2img", "prompt": "Lakeside at dawn in mist, ink painting style", "aspect_ratio": "16:9", "size": "2K"}

// Image-to-image
{"action": "img2img", "prompt": "Transform the scene into a cyberpunk style with neon lights", "image_urls": ["uploads/3/photo.jpg"]}

// Image editing
{"action": "edit", "prompt": "Change the sky to a starry sky, keep the person unchanged", "image_urls": ["https://example.com/original.jpg"]}
```

**Pre-call**: If description is vague, ask for subject; optionally offer style/ratio via notify(options). If description is clear, call directly.

**Configuration** (`.env` or environment variables):
- `NANO_BANANA_API_KEY`: Bilibili LLM API key (required)
- `NANO_BANANA_BASE_URL`: API base URL; default `http://llmapi.bilibili.co/v1`
- `NANO_BANANA_MODEL`: Model name; default `ppio/nano-banana-pro`

---

## notify

**Purpose**: Unified UI notification entry. Merges `send_progress` / `send_options` / `send_confirm` into a single tool dispatched by `action`.

| action | Behavior | Required params | DB persisted? |
|--------|----------|-----------------|---------------|
| `progress` | Push progress message to frontend, continue immediately | `message` | No (pure UI) |
| `options` | Show choice buttons, **STOP and wait for user reply** | `title`, `options` | Yes |
| `confirm` | Show confirm/cancel dialog, **STOP and wait for user reply** | `message` | Yes |

**Parameters**:
- `action` (string, required): `progress` / `options` / `confirm`
- `message` (string): progress text or confirmation description
- `title` (string): title shown above option buttons (options only)
- `options` (array): `[{"label": "...", "value": "..."}, ...]` (options only)
- `confirm_label` (string, optional): confirm button label, default `"确认"`
- `cancel_label` (string, optional): cancel button label, default `"取消"`

**Examples**:
```json
{"action": "progress", "message": "正在分析数据..."}

{"action": "options", "title": "请选择输出格式", "options": [{"label": "Markdown", "value": "md"}, {"label": "Excel", "value": "xlsx"}]}

{"action": "confirm", "message": "即将删除文件 foo.txt，此操作不可撤销。确认继续？", "confirm_label": "确认删除", "cancel_label": "取消"}
```

---

## skill

**Purpose**: Unified skill operations entry. Merges `get_skill_content` / `run_script` / `read_asset` / `write_skill_file` / `reload_skills` into a single tool dispatched by `action`.

> **⚠️ Never use `fs` to access skill files.** The internal directory layout (`system/` vs `users/`) is not exposed — always use `skill(action=read_asset)`, `skill(action=read_reference)`, or `skill(action=write)` to read/write skill content.

**`write` sub_path rules**:
- **Omit `sub_path`** → writes `SKILL.md` (do NOT pass `sub_path="SKILL.md"`)
- `sub_path="script/foo.sh"` → writes to `script/`
- `sub_path="assets/bar.png"` → writes to `assets/`
- `sub_path="references/baz.md"` → writes to `references/`

**Action overview**:

| action | Required parameters | Description |
|--------|----------|------|
| `load` | `skill_id` | Load the full content of a skill. **Must be called before executing any skill.** |
| `run_script` | `skill_id`, `script_name` | Execute a script in the skill's `script/` directory. Auto-selects interpreter by extension (.sh→bash, .py→python3, .js→node). 60s timeout. Optional `args` (string array). |
| `read_asset` | `skill_id`, `asset_name` | Read a file from the skill's `assets/` directory (asset files: images, generated outputs, binary data, etc.). `asset_name` is relative to `assets/`. |
| `read_reference` | `skill_id`, `reference_name` | Read a file from the skill's `references/` directory (reference files: style guides, templates, spec docs, examples, etc.). `reference_name` is relative to `references/`. |
| `write` | `skill_id`, `content` | Write a skill file. Omit `sub_path` to write `SKILL.md` (new skill); use `sub_path="script/foo.sh"`, `sub_path="assets/bar.json"`, or `sub_path="references/baz.md"` for other files. `skill_id`: lowercase letters/digits/underscores only. Cannot overwrite existing `SKILL.md`. **Must call `skill(action=reload)` after writing SKILL.md.** |
| `reload` | — | Reload all skills to make newly written skills available immediately. |

**Examples**:
```json
// Load skill content before executing
{"action": "load", "skill_id": "weekly_report"}

// Run a script in the skill's script/ directory
{"action": "run_script", "skill_id": "data_export", "script_name": "export.py", "args": ["--format", "csv"]}

// Read a reference asset file
{"action": "read_asset", "skill_id": "weekly_report", "asset_name": "template.md"}

// Create a new skill (write SKILL.md, then reload)
{"action": "write", "skill_id": "my_skill", "content": "...SKILL.md content..."}
{"action": "reload"}

// Add a script to an existing skill
{"action": "write", "skill_id": "my_skill", "content": "#!/bin/bash\necho hello", "sub_path": "script/run.sh"}
```

---

## tool_request

**Purpose**: Report and track missing tool capabilities. When you need a tool that does not exist, submit a request here so the developer can implement it.

**Action overview**:

| action | Required params | Description |
|--------|-----------------|-------------|
| `request` | `name`, `description` | Submit a new missing-tool request |
| `list` | — | Query all historical requests (filter by status) |
| `close` | `id` | Mark a request as implemented / no longer needed |

**action: "request"** — Report a missing capability
- `name` (string, required): Tool name in snake_case, e.g. `send_email`
- `description` (string, required): One-line summary of what the tool should do
- `trigger` (string, optional): The user request or scenario that triggered this need
- `input_schema` (string, optional): Expected input parameters (free-text description or JSON Schema)
- `output_schema` (string, optional): Expected return value format

**action: "list"** — Query history
- `status` (string, optional): Filter by `pending` / `done`; omit to return all

**action: "close"** — Mark as resolved
- `id` (integer, required): Record ID from the `list` result
- `reason` (string, optional): Why it's being closed (e.g. "covered by existing `exec` tool")

**Examples**:
```json
// Report a missing tool
{"action": "request", "name": "send_email", "description": "Send an email via SMTP", "trigger": "User asked to send a report by email"}

// Check all pending requests
{"action": "list", "status": "pending"}

// Close a resolved request
{"action": "close", "id": 3, "reason": "Covered by feishu(action=send)"}
```

---

## output_file

**Purpose**: Write content to a file and get a download URL in a single call, or generate a download URL for an existing server file.

| action | Required params | Returns | Description |
|--------|-----------------|---------|-------------|
| `write` | `filename`, `content` | `path`, `rel_path`, `download_url`, `expires_in` | Write text to `output/` and auto-generate a temporary download token |
| `download` | `file_path` | `download_url`, `expires_in` | Generate a temporary download token for an already-existing server file |

**Parameters**:
- `action` (string, required): `write` or `download`
- `filename` (string): `write`: output filename with extension, e.g. `report_2026.txt`; `download`: optional display filename shown to user on download
- `content` (string): Text content to save — required for `write`
- `file_path` (string): Absolute or relative server path of an existing file — required for `download`

**Examples**:
```json
// Write a report and get download URL in one call
{"action": "write", "filename": "weekly_report_2026-03-15.md", "content": "# Weekly Report\n..."}
// Returns: {"path": "/app/output/A/weekly_report_2026-03-15.md", "rel_path": "output/A/...", "download_url": "/download/abc123", "expires_in": 1800}

// Generate download link for an existing file (e.g. produced by exec or skill script)
{"action": "download", "file_path": "/app/output/B/export.csv", "filename": "export.csv"}
```

**Notes**:
- Present `download_url` as a Markdown link using the **exact value returned**: `[Download](/download/abc123)` — **never prepend a domain or construct an absolute URL.**
- Tokens expire after `expires_in` seconds (default 30 min).

---

## mcp

**Purpose**: Access external MCP (Model Context Protocol) servers. Provides a lazy-loading three-tier strategy: the system prompt only shows matched server summaries (tool names), and full schemas are fetched on demand.

**Action overview**:

| action | Required params | Description |
|--------|-----------------|-------------|
| `list` | — | List all configured MCP servers and their tools (with descriptions) |
| `detail` | `server`, `tool` | Get the full inputSchema for a specific tool |
| `call` | `server`, `tool`, `args` | Execute a tool on the specified MCP server |

**Parameters**:
- `action` (string, required): `list` / `detail` / `call`
- `server` (string): MCP server name — required for `detail` / `call`
- `tool` (string): Tool name within the server — required for `detail` / `call`
- `args` (object): Tool arguments as key-value pairs — required for `call`

**Workflow**:
1. The system prompt's `# MCP Servers` section shows matched servers and their tool name lists
2. If tool arguments are clear, call directly: `mcp(action=call, server="...", tool="...", args={...})`
3. If the argument schema is unclear, first call `mcp(action=detail, ...)` to inspect the inputSchema, then call
4. To see all available servers and tools, call `mcp(action=list)`

**Examples**:
```json
// List all servers and tools
{"action": "list"}

// Get full schema for a specific tool
{"action": "detail", "server": "github", "tool": "list_prs"}

// Call a tool
{"action": "call", "server": "github", "tool": "list_prs", "args": {"state": "open", "repo": "owner/repo"}}

// Call filesystem tool
{"action": "call", "server": "filesystem", "tool": "read_file", "args": {"path": "/tmp/data.txt"}}
```

**Notes**:
- MCP tools disabled if `config/mcp.json` is missing — `action=list` returns an error message
- For stdio transport, the MCP server process is started lazily on first use
- Tool list is cached for 5 minutes per server; call `mcp(action=list)` to refresh

---

## Usage Guidelines
1. **notify(progress)**: Proactively push progress on waits and multi-step ops.
2. **Stop after interactive tools**: After notify(options/confirm) or send_file_upload, stop — wait for user reply next turn.
3. **notify(confirm) before irreversible ops** (delete, send, submit).
4. **run_script/read_asset/read_reference**: Only when SKILL.md explicitly instructs; do not self-invoke.
5. **KV**: set=overwrite, append=accumulate. Write results to KV at skill end; check KV at skill start before reprocessing.
6. **On tool error**: explain to user.
7. **skill(reload) after skill(write)**: must call immediately after; otherwise new skill won't load.
8. **[File: ...] in message**: call read_file immediately before replying.
9. **tool_request**: check list before submitting (avoid duplicates); auto-close pending items already covered by existing tools.
10. **output_file(write)**: persist generated files and return download_url; (download) only for pre-created files. Never output large content inline.
11. **browser(close)**: always call after completing a browser task.
12. **web_fetch first**: try before browser; use browser only when JS rendering is required.
13. **Read Office/PDF**: read_file for .docx/.pptx/.xlsx; read_pdf for .pdf. Never exec/Python libs.
14. **Write Office/PDF**: output_file(write, filename="xxx.docx/xlsx/pptx/pdf") — server converts by extension. Never exec/Python libs.
