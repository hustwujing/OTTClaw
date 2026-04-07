# Available Tools

## exec

Execute a shell command via `bash -c`.

**主 agent 会话（必须两步）：**
1. `exec(command)` → 推送确认框给用户，返回 `{status:"pending_approval", pending_id:"ep_xxx"}` — **命令尚未执行！Stop and wait.**
2. 用户点击"确认执行"后，**必须调用 `exec_run(pending_id:"ep_xxx")`** → 返回 `{status:"done"/"running", exit_code, output}` — 命令至此才真正执行。

**⚠️ 禁止：** 看到 `pending_approval` 后假设命令已完成、跳过 `exec_run` 直接调 `output_file`。下一轮务必先调 `exec_run(pending_id)`，再处理生成的文件。

**子 agent（spawn_subagent）中：** exec 跳过审批步骤，直接执行。

| Param | Default | Notes |
|-------|---------|-------|
| `command` | **required** | Shell command |
| `workdir` | server cwd | Working directory |
| `env` | — | Extra env vars `{"K":"V"}` |
| `timeout_sec` | 1800 | Hard timeout (seconds) |
| `yield_ms` | 10000 | Wait window before backgrounding |
| `background` | false | `true` = skip wait, background immediately |

exec_run returns inline: `{status:"done", exit_code, output}` | Still running: `{status:"running", session_id}` — poll with `process(action=poll)`

**⚠️ File auto-delivery after exec** — no separate `output_file` call needed if:

| Method | How | Scope |
|--------|-----|-------|
| **A (recommended)** | Save to `$AGENT_OUTPUT_DIR` | Any file, any count |
| B | Append path to `$AGENT_REGISTER_FILE` | Files saved outside AGENT_OUTPUT_DIR |
| C | Save directly to `output/<filename>` | Images only |
| D (fallback) | `output_file(action=download, file_path=...)` | Any file |

**Method A — AGENT_OUTPUT_DIR（首选）**

exec 启动时自动注入 `AGENT_OUTPUT_DIR`（session 级隔离目录，每次 exec 唯一）。**所有生成的文件都保存到这个目录**，exec 结束后系统自动扫描并投递，无需任何注册代码。

```python
# Python — 保存单个文件
import os
out_dir = os.environ['AGENT_OUTPUT_DIR']
output_path = os.path.join(out_dir, 'chart.png')
plt.savefig(output_path)
```

```python
# Python — 保存多个文件
import os
out_dir = os.environ['AGENT_OUTPUT_DIR']
plt.savefig(os.path.join(out_dir, 'chart.png'))
df.to_csv(os.path.join(out_dir, 'data.csv'), index=False)
with open(os.path.join(out_dir, 'report.txt'), 'w') as f:
    f.write(report_content)
```

```bash
# Bash
out_dir="$AGENT_OUTPUT_DIR"
cp result.pdf "$out_dir/result.pdf"
```

```javascript
// Node.js
const outDir = process.env.AGENT_OUTPUT_DIR;
fs.writeFileSync(path.join(outDir, 'output.json'), JSON.stringify(data));
```

**Method B — AGENT_REGISTER_FILE（兜底：文件必须保存到其他位置时）**

每生成一个文件，追加其绝对路径到 `$AGENT_REGISTER_FILE`（一行一个）：

```python
import os
reg = os.environ.get('AGENT_REGISTER_FILE', '')
if reg:
    open(reg, 'a').write('/path/to/file.png\n')
```

After A/B/C: exec result contains `imageSentNote` (embed markdown verbatim, don't alter URL) or `filesSentNote` (include `download_url` in reply).

**⚠️ matplotlib 中文字体** — 每个含中文的 matplotlib 脚本，**开头必须粘贴下面这段 preamble**，之后所有标题/坐标轴/图例/注解自动生效，无需对每个元素单独设置：

```python
import os, matplotlib, matplotlib.font_manager as _fm

def _setup_cn_font():
    for _p in [
        '/System/Library/Fonts/Hiragino Sans GB.ttc',         # macOS
        '/System/Library/Fonts/STHeiti Medium.ttc',            # macOS
        '/System/Library/Fonts/PingFang.ttc',                  # macOS
        '/Library/Fonts/Arial Unicode MS.ttf',                 # macOS
        'C:/Windows/Fonts/msyh.ttc',                           # Windows
        'C:/Windows/Fonts/simhei.ttf',                         # Windows
        '/usr/share/fonts/truetype/wqy/wqy-microhei.ttc',     # Linux
        '/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc',  # Linux
        '/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc',  # Linux
    ]:
        if os.path.exists(_p):
            _fm.fontManager.addfont(_p)
            matplotlib.rcParams['font.sans-serif'] = [_fm.FontProperties(fname=_p).get_name()] + matplotlib.rcParams['font.sans-serif']
            matplotlib.rcParams['axes.unicode_minus'] = False
            return
    for _f in _fm.fontManager.ttflist:  # fallback: search font cache
        if any(_k in _f.name for _k in ('CJK', 'Heiti', 'YaHei', 'WenQuanYi', 'Hiragino', 'PingFang', 'STHeiti')):
            matplotlib.rcParams['font.sans-serif'] = [_f.name] + matplotlib.rcParams['font.sans-serif']
            matplotlib.rcParams['axes.unicode_minus'] = False
            return

_setup_cn_font()
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

**Most common call** (copy verbatim, replace values only):
```json
{"action":"send","receive_id":"self","text":"消息内容"}
```

Parameters:
- `receive_id` (string, **required**): Recipient ID, or `"self"` to use the user's bound open_id
- `text` (string): Text content (**required** unless `file_path` is provided)
- `receive_id_type` (string, optional): `open_id` (default) / `user_id` / `chat_id` / `union_id`; auto-inferred from prefix when `"self"` is used
- `file_path` (string): Local file path; images auto-detected and uploaded

**Do NOT use** `content`, `msg_type`, `payload` — these are Feishu Open API fields, not this tool's parameters. Pass the message text directly as `text="消息内容"`.

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

**Purpose**: Explore codebase and docs. 8 actions covering all code analysis needs — always prefer these over `fs` or `exec` for reading/searching source files.

**Common parameters**:
- `action` (string, required): one of `tree / grep / glob / outline / chunk_read / git / ast_grep / comby`
- `path` (string, required): target file or directory

---

**action: "tree"** — Recursively list directory structure
- `max_depth` (int, optional): recursion depth, default 5
- `include` (string, optional): filename glob filter, e.g. `"*.go"`

Use when: getting an overview of project layout.
```
code_search(action="tree", path="internal/")
```

---

**action: "grep"** — Regex search across file contents
- `pattern` (string, required): regex, e.g. `"func handleExec"`
- `include` (string, optional): filename filter, e.g. `"*.go"`
- `max_results` (int, optional): default 50
- `context_lines` (int, optional): lines before/after each match, default 2

Use when: finding where a symbol/string is defined or used.
```
code_search(action="grep", path=".", pattern="func handleExec", include="*.go")
code_search(action="grep", path=".", pattern="e\\.register\\(", include="*.go")
```

---

**action: "glob"** — Find files by name pattern (supports `**`)
- `pattern` (string, required): glob pattern, e.g. `"**/*.go"`, `"internal/**/*_test.go"`
- `max_results` (int, optional): default 300

Use when: locating files by naming convention, e.g. all test files, all config files.
```
code_search(action="glob", path=".", pattern="**/*_test.go")
code_search(action="glob", path=".", pattern="internal/tool/*.go")
```

---

**action: "outline"** — Extract symbol structure without reading full file
- `path`: file or directory
- `include` (string, optional): filename filter when path is a directory

Supports: Go (func/struct/interface/type), Python (def/class), TS/JS (function/class/interface/type), Rust (fn/struct/enum/trait/impl), Java (class/interface/method), Markdown (headings). Returns line numbers for all symbols.

Use when: understanding a file's structure before deciding which section to read; finding which function is at a given line range.
```
code_search(action="outline", path="internal/agent/agent.go")
code_search(action="outline", path="internal/tool/")
```

---

**action: "chunk_read"** — Read large files in sections with line numbers
- `chunk` (int, optional): which chunk to read, 1-based, default 1
- `chunk_size` (int, optional): lines per chunk, default 80

Result header: `[chunk N/M | lines X-Y of Z | filename]`. If more chunks remain, the result ends with `[K chunks remaining — call chunk_read with chunk=N+1 to continue reading]`. **Keep calling with chunk=2, 3, … until you see `[end of file]`**.

Use when: reading a large file section by section (use `outline` first to find the target line range, then jump to the right chunk).
```
code_search(action="chunk_read", path="internal/agent/agent.go", chunk=1)
code_search(action="chunk_read", path="internal/agent/agent.go", chunk=3, chunk_size=120)
```

---

**action: "git"** — Read-only git history operations
- `git_action` (string, required): `log | blame | diff | show | status | branch | tag`
- `revision` (string, optional): commit ref / range (for diff/show/blame/log)
- `pattern` (string, optional): file path (blame/diff) or `--grep` keyword (log)
- `n` (int, optional): max entries for log, default 20

Use when: simple git lookups — basic log, show a commit, diff a revision, blame a file, check status. For complex queries needing date arithmetic, author filters, pipes, custom formats, or multi-command chains, use exec instead.
```
code_search(action="git", path=".", git_action="log", n=10)
code_search(action="git", path=".", git_action="blame", pattern="internal/agent/agent.go")
code_search(action="git", path=".", git_action="diff", revision="HEAD~1")
code_search(action="git", path=".", git_action="show", revision="abc1234")
```

---

**action: "ast_grep"** — Structural code search by AST pattern (requires `ast-grep`)
- `pattern` (string, required): code template with `$VAR` / `$$$VARS` placeholders
- `lang` (string, required): `go / python / js / ts / rust / java / cpp / ...`
- `max_results` (int, optional): default 50
- `context_lines` (int, optional): context lines around each match

Matches syntax structure, not text — finds all calls regardless of formatting or argument names. Install: `brew install ast-grep` / `cargo install ast-grep`.

Use when: finding all callers of a function, all error-handling patterns, all struct instantiations of a type.
```
code_search(action="ast_grep", path=".", pattern="fmt.Errorf($$$)", lang="go")
code_search(action="ast_grep", path=".", pattern="if $ERR != nil { $$$BODY }", lang="go")
code_search(action="ast_grep", path=".", pattern="useState($INIT)", lang="ts")
```

---

**action: "comby"** — Delimiter-balanced template search, language-agnostic (requires `comby`)
- `pattern` (string, required): template with `:[VAR]` placeholders; brackets/quotes auto-balanced
- `include` (string, optional): file extension matcher, e.g. `".go"`
- `max_results` (int, optional): default 50

Unlike regex, `:[VAR]` matches any nested content including parentheses and brackets. Install: `brew install comby` / `bash <(curl -sL get.comby.dev)`.

Use when: grep regex is too rigid for nested expressions; cross-language structural patterns.
```
code_search(action="comby", path=".", pattern="fmt.Sprintf(:[fmt], :[args])", include=".go")
code_search(action="comby", path=".", pattern="json.Unmarshal(:[data], &:[target])", include=".go")
```

---

## cron

**Purpose**: Create and manage scheduled tasks. When a task is due, the system automatically creates an independent session in the background and runs the agent, using `message` as the user prompt.

**`message` is an agent prompt, NOT a delivery payload.**
The agent that runs at trigger time has full access to all tools (feishu, exec, browser, etc.).
Write `message` as a natural-language instruction describing what the agent should DO.
→ To send a Feishu notification: write `message` as "用飞书发消息给 self：<内容>"
→ Do NOT use `payload`, `description`, `task_type`, or `receive_id` — these parameters do not exist.

**`schedule` must be a JSON object** (one of the three formats below). Never pass a raw cron string like `"0 22 * * *"`.

**Action list**:

| action | Required parameters | Description |
|--------|----------|------|
| `status` | — | Returns scheduler status and `running_jobs` list (job_id, started_at, elapsed_ms for each running job) |
| `list` | — | Lists all scheduled tasks for the current user |
| `add` | `name`, `schedule`, `message` | Create a new task |
| `update` | `id` | Modify a task (optionally update name/schedule/message/enabled) |
| `remove` | `id` | Delete a task |
| `run` | `id` | Trigger once immediately (runs in background, does not wait for result) |
| `cancel` | `id` | Send a cancellation signal to a currently-running job. Returns `{ok: true, was_running: true}` if the signal was sent, or `{ok: false, was_running: false}` if the job was not running. The goroutine exits after its current LLM call; history status transitions to `cancelled`. |
| `history` | — | Query recent run records. Optional: `id` (filter by job), `limit` (default 20, max 100). Returns list with `status` (running/succeeded/failed/timed_out/cancelled), `started_at`, `ended_at`, `error_msg`. |

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
// Every night at 22:00 — send a Feishu message to self
{"action":"add","name":"每日22点睡觉提醒","schedule":{"kind":"cron","expr":"0 22 * * *","tz":"Asia/Shanghai"},"message":"用飞书发消息给 self：你好，该睡觉了"}

// Daily reminder at 9 AM (Beijing time (CST))
{"action":"add","name":"Morning Reminder","schedule":{"kind":"cron","expr":"0 9 * * *","tz":"Asia/Shanghai"},"message":"Please remind me of today's to-do items"}

// Hourly reminder to drink water
{"action":"add","name":"Water Reminder","schedule":{"kind":"every","everyMs":3600000},"message":"Remind me to drink water and give me a health tip"}

// Single execution at Beijing time (CST) 2026-03-20 09:00
{"action":"add","name":"Meeting Reminder","schedule":{"kind":"at","at":"2026-03-20T09:00:00+08:00"},"message":"Remind me to attend today's product review meeting"}
```

---


## spawn_subagent

Delegate a subtask to a background subagent. Returns `task_id` immediately; result auto-injected when done. Use for complex/long/parallel tasks — even a single long-running task warrants spawning.

- `task` (string, required): Task prompt for the subagent
- `label` (string): Short name shown in notifications — always set (e.g. `"分析销售数据"`)
- `context` (string): Background info appended to subagent's prompt
- `notify_policy`: `done_only` (default) | `state_changes` (also on start) | `silent` (never)
- `retain_hours` (integer): Hours to retain record after terminal state; 0 = global default (~72 h)

Returns: `{task_id, child_session_id, status: "queued"}`

```json
{"task": "Analyze /tmp/sales.csv, return top-10 by revenue", "label": "分析销售数据", "context": "Q1 2026 only"}
{"task": "Scrape product prices from URL", "label": "爬取价格数据"}
{"task": "Prepare HTML report template", "label": "准备报告模板"}
{"task": "Send Feishu to ou_xxx: 'Report ready'", "label": "发送完成通知", "notify_policy": "silent"}
{"task": "Run regression test suite on /app", "label": "回归测试", "notify_policy": "state_changes"}
```

---

## cancel_subtask

Cancel a queued/running subagent task. `force=false` (default): graceful → `cancelled` after current LLM call. `force=true`: immediate DB update → `killed`.

- `task_id` (integer, required)
- `reason` (string, optional): recorded in `error_msg`
- `force` (boolean, optional)

Returns: `{status: "cancelling"|"killed"|"<terminal>", note?}`

```json
{"task_id": 7, "reason": "User changed requirements"}
{"task_id": 7, "force": true, "reason": "Task unresponsive"}
```

---

## report_task_progress

*(Subagent-only)* Write current progress to DB `progress_summary`. Lightweight — DB update only, no LLM overhead. Call after each major step. Use `notify_parent` when the parent must act immediately.

- `progress` (string, required)

```json
{"progress": "Data collection complete (50 records), starting classification"}
```

---

## notify_parent

*(Subagent-only)* Inject a message into the parent session and trigger a new parent LLM turn — use when the parent must *act* now (critical finding, decision needed). Returns immediately; parent runs async. For routine updates use `report_task_progress`.

- `message` (string, required)

```json
{"message": "Phase 1 done: 200 records, 3 anomalies found — recommend deciding whether to proceed"}
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
- `web_url`: Relative path of the image

**Delivery**: Image is automatically sent to the user's channel (web inline / Feishu / WeCom / WeChat). The tool result will confirm delivery. **Do NOT embed image markdown or URL in your reply** — just describe the result in plain text.

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

> **⚠️ Never use `fs` to access skill files.** Always use `skill()` actions — they handle path resolution automatically.

**Skill directory structure** (three tiers):

| Tier | Path | Access |
|------|------|--------|
| System skills | `skills/system/<skill_id>/` | All users, **read-only** |
| User private skills | `skills/users/<userid>/<skill_id>/` | Owner only, read/write |
| Agent self-improving skills | `skills/users/<userid>/self-improving/skills/<skill_id>/` | Owner only, read/write |

**Read priority** (`run_script` / `read_script` / `read_asset` / `read_reference` search in this order):
1. `skills/users/<userid>/<skill_id>/`
2. `skills/users/<userid>/self-improving/skills/<skill_id>/`
3. `skills/system/<skill_id>/`

User-crafted skills always override self-improving skills with the same `skill_id`. If not found in any tier, an error is returned listing all paths searched.

**Write destination**:
- `skill(action=write)` (normal) → `skills/users/<userid>/<skill_id>/`
- Self-improving write → `skills/users/<userid>/self-improving/skills/<skill_id>/`
- `skills/system/` is **always read-only** — writes are rejected.

**`write` sub_path rules**:
- **Omit `sub_path`** → writes `SKILL.md` (do NOT pass `sub_path="SKILL.md"`)
- `sub_path="script/foo.sh"` → writes to `script/`
- `sub_path="assets/bar.png"` → writes to `assets/`
- `sub_path="references/baz.md"` → writes to `references/`

**Action overview**:

| action | Required parameters | Description |
|--------|----------|------|
| `load` | `skill_id` | Load the full content of a skill. **Must be called before executing any skill.** |
| `run_script` | `skill_id`, `script_name` | Execute `script/<script_name>` in the skill directory (read priority: user → self-improving → system). Auto-selects interpreter by extension (.sh→bash, .py→python3, .js→node). 60s timeout. Optional `args` (string array). |
| `read_script` | `skill_id`, `script_name` | Read the source content of `script/<script_name>` without executing it (read priority: user → self-improving → system). Useful for reviewing or editing existing scripts. |
| `read_asset` | `skill_id`, `asset_name` | Read `assets/<asset_name>` from the skill directory (read priority: user → self-improving → system). |
| `read_reference` | `skill_id`, `reference_name` | Read `references/<reference_name>` from the skill directory (read priority: user → self-improving → system). |
| `write` | `skill_id`, `content` | Write a skill file to `skills/users/<userid>/<skill_id>/`. Omit `sub_path` to write `SKILL.md`; use `sub_path="script/foo.sh"` / `"assets/bar.json"` / `"references/baz.md"` for other files. `skill_id`: lowercase letters/digits/underscores only. **Must call `skill(action=reload)` after writing SKILL.md.** |
| `delete` | `skill_id` | Delete a user-owned skill directory and auto-reload the store. Searches both `skills/users/<userid>/<skill_id>/` and `skills/users/<userid>/self-improving/skills/<skill_id>/`; deletes whichever exists. **System skills (`skills/system/`) cannot be deleted.** |
| `reload` | — | Reload all skills to make newly written skills available immediately. |

**Examples**:
```json
// Load skill content before executing
{"action": "load", "skill_id": "weekly_report"}

// Run a script in the skill's script/ directory
{"action": "run_script", "skill_id": "data_export", "script_name": "export.py", "args": ["--format", "csv"]}

// Read a script file's source without executing it
{"action": "read_script", "skill_id": "data_export", "script_name": "export.py"}

// Read a reference asset file
{"action": "read_asset", "skill_id": "weekly_report", "asset_name": "template.md"}

// Create a new skill (write SKILL.md, then reload)
{"action": "write", "skill_id": "my_skill", "content": "...SKILL.md content..."}
{"action": "reload"}

// Add a script to an existing skill
{"action": "write", "skill_id": "my_skill", "content": "#!/bin/bash\necho hello", "sub_path": "script/run.sh"}

// Delete a user-owned skill (auto-reloads store)
{"action": "delete", "skill_id": "my_skill"}
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

## fs

**Purpose**: File system operations inside the project sandbox. Prefer `code_search` for reading code/doc files.

**Action overview**:

| action | Required params | Description |
|--------|-----------------|-------------|
| `list` | `path` | List directory contents |
| `stat` | `path` | File/dir metadata (size, mtime, type) |
| `read` | `path` | Read file; images → multimodal; text max 512 KB |
| `write` | `path`, `content` | Write (or append) to file |
| `delete` | `path` | Delete file or directory |
| `move` | `src`, `dst` | Move/rename |
| `mkdir` | `path` | Create directory (parents auto-created) |

**Parameters**:
- `action` (string, required): one of the actions above
- `path` (string): target path — required for all actions except `move`
- `content` (string): file content — required for `write`
- `append` (boolean): `write` only — true to append instead of overwrite
- `recursive` (boolean): `delete` only — true to delete non-empty directory
- `src` / `dst` (string): `move` only — source and destination paths

**Path restrictions**:

| Directory | read | write/delete/move/mkdir |
|-----------|------|------------------------|
| `uploads/` | ✅ | ✅ |
| `output/` | ✅ | ✅ |
| `skills/users/{userID}/` | ✅ | ✅ (own user only) |
| `/tmp/` | ✅ | ✅ |
| `extra_fs_dirs` (configured) | ✅ | ✅ |
| `skills/system/` | ✅ | ❌ always read-only |
| `.env`, `*.db`, `data/` | ❌ blocked | ❌ blocked |

**Examples**:
```json
{"action": "list", "path": "output/"}
{"action": "read", "path": "uploads/3/report.txt"}
{"action": "write", "path": "/tmp/my_skill_abc123/result.json", "content": "{\"ok\":true}"}
{"action": "write", "path": "output/notes.md", "content": "more content\n", "append": true}
{"action": "delete", "path": "/tmp/my_skill_abc123", "recursive": true}
{"action": "move", "src": "output/draft.md", "dst": "output/final.md"}
{"action": "mkdir", "path": "/tmp/my_skill_abc123"}
```

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

## kv

Session scratch space — **lost when session ends**. For passing intermediate data between steps within one session only.

| action | behavior |
|--------|----------|
| `get` | returns value, or null if missing |
| `set` | overwrite (any JSON type) |
| `append` | add element to array; creates array if absent |

---

## memory

Cross-session persistence. Three targets:

| target | what to store | actions |
|--------|---------------|---------|
| `notes` | agent scratchpad — env facts, tool quirks, conventions (§-separated) | get / add / replace / remove |
| `persona` | user profile — name, role, communication style | get / add / replace / remove |
| `user_kv` | user-scoped business data that outlives sessions — not profile info (→ persona), not agent internals (→ notes); e.g. settings, task states, last-run timestamps | get / set / remove / list |

`user_kv` key format: letters/digits/`_:.-`, max 200 chars. Namespace to avoid collisions: `"feature:attr"`.
`user_kv` entry limit: configurable via `MEMORY_SKILL_KV_ENTRY_LIMIT` (default 200); no auto-cleanup — remove stale keys manually when limit is reached.

---

## get_session_info

**Purpose**: Return the current context identifiers. Call when you need `user_id` or `session_id` — typically to construct user-isolated paths or pass IDs to external systems.

**No parameters required.**

**Returns** (JSON object):

| Field | Always present | Description |
|-------|----------------|-------------|
| `user_id` | ✅ | Current user identifier |
| `session_id` | ✅ | Current session identifier |
| `session_source` | ✅ | Channel: `web` or `feishu` |
| `session_title` | only when set | AI-generated title of this session |
| `parent_session_id` | only when set | Present in continuation sessions |

**Primary use case — temp dir isolation in LLM-direct skills**:

When a skill step runs without a script (LLM executes exec directly), always use `session_id`
to construct a per-user isolated working path — never use a shared fixed path:

```
/tmp/{skill_id}_{session_id}/
```

> **Note**: Scripts invoked via `skill(action=run_script)` automatically receive
> `SKILL_SESSION_ID` and `SKILL_USER_ID` as environment variables — no need to call
> `get_session_info` from within scripts.

---

## Usage Guidelines
1. **notify(progress)**: Proactively push progress on waits and multi-step ops.
2. **Stop after interactive tools**: After notify(options/confirm/upload), stop — wait for user reply next turn.
3. **notify(confirm) before irreversible ops** (delete, send, submit).
4. **skill execution flow**: Always call `skill(action=load)` first → read the returned SKILL.md content and available file listing → follow the steps as written → only call `skill(action=run_script/read_file`) when the loaded SKILL.md explicitly directs it. Never invoke these actions without loading first.
5. **kv vs memory(user_kv)**: `kv` is session-scoped — use for inter-step scratch within one session. `memory(target=user_kv)` persists across sessions — use for user-level business data. Never use `kv` for data that must survive session close.
6. **On tool error**: explain to user.
7. **skill(reload) after skill(write)**: must call immediately after; otherwise new skill won't load.
8. **tool_request**: check list before submitting (avoid duplicates); auto-close pending items already covered by existing tools.
9. **output_file(write)**: persist generated files and return download_url; (download) only for pre-created files. Never output large content inline.
10. **browser(close)**: always call after completing a browser task.
11. **web_fetch first**: try before browser; use browser only when JS rendering is required.
12. **Read files**: use read_file for all types — .docx/.pptx/.xlsx (text), .pdf (add pages="1-5" to select range or render=true for scanned), images .jpg/.png/.gif/.webp (visual analysis). Never exec/Python libs.
13. **Write Office/PDF**: output_file(write, filename="xxx.docx/xlsx/pptx/pdf") — server converts by extension. Never exec/Python libs.
14. **Files produced by exec_run**: ⚠️ 只有 `exec_run` 返回成功后（`status:"done"`），文件才真正存在。**禁止**在 `exec` 返回 `pending_approval` 后立即处理文件——此时命令尚未执行。exec_run 返回后：优先方案 A（脚本 `print(abs_path)`）或方案 B（保存到 `output/` 目录）会触发系统自动投递图片，结果中含 `imageSentNote`（按指示嵌入 markdown）或 `filesSentNote`（含 `download_url`）。仅当自动投递未触发时，才调 `output_file(action=download, file_path=<path>)` 兜底——对图片同样触发自动投递，对其他文件返回 `download_url` 供用户下载。
15. **Temp dir isolation**: In LLM-direct skill steps (no script), call `get_session_info` first and use `session_id` to build an isolated path: `/tmp/{skill_id}_{session_id}/`. Never use a hardcoded shared `/tmp/xxx` path.
16. **spawn_subagent pattern**: Spawn when a task is complex/time-consuming, or to fan out parallel subtasks (one call per task); immediately call `notify(action=progress)`; then **stop and wait** — system injects a batch result when all tasks finish. Use `cancel_subtask` to abort; check returned `status` — if already terminal, no action needed.
    **label**: Always pass a short label (2–8 words) for readable notifications. Without: `#7`; with: `#7「Competitor research」`.
    **notify_policy**: `done_only` (default) when result needed; `silent` for fire-and-forget; `state_changes` for long tasks where "started" feedback matters.
    **retain_hours**: Set only when result must survive beyond global retention (e.g. 7-day report). Leave 0 otherwise.
    **cancel_subtask force**: Prefer `force=false` (graceful → `cancelled`); use `force=true` only when unresponsive (immediate → `killed`).
    **Batch results**: Process all subagent results in one reply. Include `![image](url)` verbatim. If `[N images auto-delivered]`, acknowledge only — don't re-embed. Present `download_url` as a Markdown link.
17. **图片投递规则**：所有产图/截图工具（`desktop screenshot`、`browser screenshot`、`nano_banana`、`exec` 生成图）都由系统自动推送到用户渠道——**不要**在回复文字中嵌入 `![](url)`，除非工具结果的 `imageSentNote` 字段明确要求嵌入指定 markdown（仅 `exec` 主 agent 场景）。若用户要求将服务器上**已有**图片文件发给自己，调 `output_file(action=download, file_path=<path>)` 即可自动投递。
18. **matplotlib 中文字体**：每一个调用 matplotlib 的 Python 脚本（无论标签是否含中文），必须在文件最顶部（`import matplotlib.pyplot` 之前）**原样复制**本文件开头的 `_get_cn_font` / `_apply_cn_font` 代码块，并在所有 axes 设置完成后调用 `_apply_cn_font(ax, cn_font)`。不允许省略，不允许改用 `rcParams['font.sans-serif']`。

---

## desktop（桌面控制，需 DESKTOP_ENABLED=true）

通过 `action` 参数分发所有桌面操作。

**Action 一览**：

| action | 描述 | 必填参数 |
|--------|------|----------|
| `screenshot` | 截取全屏；图片**自动投递**到用户渠道（web 内联 / 飞书 / 企微 / 微信原生图片），LLM 同步可见用于分析 | — |
| `get_screen_size` | 返回屏幕分辨率（如 `1920x1080`） | — |
| `mouse_move` | 移动鼠标到指定坐标 | `x`, `y` |
| `left_click` | 左键单击 | `x`, `y` |
| `right_click` | 右键单击 | `x`, `y` |
| `double_click` | 左键双击 | `x`, `y` |
| `type` | 输入文本 | `text` |
| `key` | 按下按键或组合键 | `key` |
| `scroll` | 滚动 | `x`, `y`, `direction`(up/down/left/right), `amount`(默认 3) |
| `drag` | 鼠标拖拽 | `start_x`, `start_y`, `end_x`, `end_y` |

**key 格式示例**：`ctrl+c`、`ctrl+v`、`ctrl+alt+t`、`Return`、`escape`、`Tab`、`BackSpace`、`F5`

**推荐工作流**：先 `screenshot` → 分析界面 → 执行操作 → 再 `screenshot` 确认结果

**投递规则**：`screenshot` 执行后图片已自动发送到用户聊天。**不要**在回复文字中嵌入图片 markdown 或 URL，用文字描述结果即可。

**平台依赖**：

| 平台 | 截图 | 鼠标/键盘 | 权限要求 |
|------|------|-----------|----------|
| macOS | 内置 `screencapture` | `brew install cliclick` | 辅助功能 + 屏幕录制（系统偏好设置手动授权） |
| Linux | `apt install scrot` | `apt install xdotool` | X11 显示（无头服务器须配置 `DISPLAY=:0` 或 Xvfb） |
| Windows | PowerShell 内置 | PowerShell 内置 | 以管理员身份运行（SendKeys 需焦点窗口） |

**使用示例**：
```json
{"action": "screenshot"}
{"action": "get_screen_size"}
{"action": "mouse_move", "x": 500, "y": 300}
{"action": "left_click", "x": 500, "y": 300}
{"action": "double_click", "x": 500, "y": 300}
{"action": "right_click", "x": 500, "y": 300}
{"action": "type", "text": "Hello, World!"}
{"action": "key", "key": "ctrl+c"}
{"action": "key", "key": "Return"}
{"action": "scroll", "x": 500, "y": 400, "direction": "down", "amount": 5}
{"action": "drag", "start_x": 100, "start_y": 100, "end_x": 400, "end_y": 200}
```
