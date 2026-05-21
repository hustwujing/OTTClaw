# Available Tools

## exec

Execute shell or Python scripts with static safety analysis. `type` required: `"shell"` (bash -c) or `"python"` (temp .py).

**Safe scripts**: run immediately → `{status:"done", exit_code, output}`.

**Dangerous scripts (two steps)**:
1. `exec(type,code)` → `{status:"pending_approval", pending_id:"ep_xxx"}` — not run yet, stop and wait.
2. After user confirms → `exec_run(pending_id:"ep_xxx")` → `{status:"done"/"running", exit_code, output}`. Never skip `exec_run`.

**Inside spawn_subagent**: always skips approval.

| Param | Default | Notes |
|-------|---------|-------|
| `type` | required | `"shell"` or `"python"` |
| `code` | required | Script source |
| `packages` | — | Python only; pip install list |
| `workdir` | server cwd | Working directory |
| `env` | — | Extra env vars `{"K":"V"}` |
| `timeout_sec` | 1800 | Seconds |
| `yield_ms` | 10000 | Wait window before backgrounding |
| `background` | false | `true` = run in background immediately |

**Dangerous patterns**: Shell: `sudo`, `rm -r`, `dd of=/dev/`, `mkfs/fdisk`, `eval $(...)`, writes to `/etc/ /dev/ /boot/`, `curl|bash`. Python: `os.system/popen/remove`, `subprocess.*`, `shutil.rmtree`, `eval()`, `exec()`, sensitive paths.

`exec_run` returns `{status:"running", session_id}` when long-running — poll with `process(action=poll)`.

**Auto file delivery** (no need to call `output_file` after exec):

**IMPORTANT — where to put files:**

- **Final output** (the user wants): save to `$AGENT_OUTPUT_DIR` → auto-delivered.
- **Intermediate/temp files** (working data, frames, partial results): save to `$AGENT_TMP_DIR` — **not** `$AGENT_OUTPUT_DIR`. These are NOT delivered to the user.

Examples of intermediate files: video frames (frame_*.jpg), temporary JSON, partial CSVs, cache files.
Examples of final files: the finished video/GIF, chart image, report PDF, processed spreadsheet.

| Method | Description |
|--------|-------------|
| **A (preferred)** | Save **final output** to `$AGENT_OUTPUT_DIR`; auto-scanned and delivered |
| B | Append absolute paths to `$AGENT_REGISTER_FILE` (one per line) |
| C | Save **final output** to `output/<filename>` (images only) |
| D (fallback) | Call `output_file(action=download, file_path=...)` |

On A/B/C success: result contains `imageSentNote` (embed as instructed) or `filesSentNote` (with `download_url`).

**Temp files**: Use `$AGENT_TMP_DIR` — never hardcode `/tmp/`.

**matplotlib CJK fonts**: Paste this preamble at the top of every matplotlib script (before `import matplotlib.pyplot`):

```python
import os, matplotlib, matplotlib.font_manager as _fm

def _setup_cn_font():
    for _p in [
        '/System/Library/Fonts/Hiragino Sans GB.ttc',
        '/System/Library/Fonts/STHeiti Medium.ttc',
        '/System/Library/Fonts/PingFang.ttc',
        '/Library/Fonts/Arial Unicode MS.ttf',
        'C:/Windows/Fonts/msyh.ttc',
        'C:/Windows/Fonts/simhei.ttf',
        '/usr/share/fonts/truetype/wqy/wqy-microhei.ttc',
        '/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc',
        '/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc',
    ]:
        if os.path.exists(_p):
            _fm.fontManager.addfont(_p)
            matplotlib.rcParams['font.sans-serif'] = [_fm.FontProperties(fname=_p).get_name()] + matplotlib.rcParams['font.sans-serif']
            matplotlib.rcParams['axes.unicode_minus'] = False
            return
    for _f in _fm.fontManager.ttflist:
        if any(_k in _f.name for _k in ('CJK', 'Heiti', 'YaHei', 'WenQuanYi', 'Hiragino', 'PingFang', 'STHeiti')):
            matplotlib.rcParams['font.sans-serif'] = [_f.name] + matplotlib.rcParams['font.sans-serif']
            matplotlib.rcParams['axes.unicode_minus'] = False
            return

_setup_cn_font()
```

**Cross-platform shell** (macOS/Linux coreutils differences):

| Command | Use instead |
|---------|-------------|
| `sed` in-place | `sed -i''` or Python |
| `date` offset | `python3 datetime` |
| `stat` file size | `python3 os.path.getsize()` |
| `base64` decode | `python3 -m base64 -d` |
| `find -printf` | `find ... -exec stat \;` or Python |
| `readlink -f` | `python3 -c "import os,sys; print(os.path.realpath(sys.argv[1]))"` |
| `grep -P` | `grep -E` or Python `re` |

---

## process

Process control for all exec sessions.

| action | Description |
|--------|-------------|
| `list` | List all sessions (id, command, status, elapsed) |
| `poll` | Wait for new output → `{status, new_output, elapsed_sec}` |
| `log` | Full output history; offset/limit paging (default: last 200 lines) |
| `write` | Write raw text to stdin (no newline) |
| `submit` | Write text to stdin + Enter (`\r`) |
| `send-keys` | Named keys: `ctrl-c`, `ctrl-d`, `enter`, `tab`, `escape`, `up`/`down`, `f1`…`f12` |
| `paste` | Multi-line text via bracketed paste |
| `kill` | Signal to process group (default `SIGTERM`; options: `SIGKILL`, `SIGINT`, `SIGHUP`) |
| `clear` | Clear incremental buffer (history kept) |
| `remove` | Remove from registry (kills first if running) |

| Param | Notes |
|-------|-------|
| `action` | Required |
| `session_id` | Required except `list` |
| `timeout` | `poll` only; ms; default 5000, max 30000 |
| `offset` | `log` start line; 0-indexed; negative = from end |
| `limit` | `log` max lines; default 200 |
| `text` | For `write`/`submit`/`paste` |
| `key` | For `send-keys` |
| `signal` | For `kill`; default `SIGTERM` |

Sessions auto-cleaned after 2 hours.

---

## feishu

Actions: `send` / `webhook` / `get_config` / `set_config`.

**send** — Feishu Bot API message

| Param | Notes |
|-------|-------|
| `receive_id` | Recipient ID or `"self"` (uses bound open_id) |
| `text` | Text content (or `file_path`) |
| `receive_id_type` | `open_id` (default) / `user_id` / `chat_id` / `union_id` |
| `file_path` | Local path; images auto-uploaded |

Never use raw API fields (`content`, `msg_type`, `payload`) — use `text="..."` directly.

**webhook** — Group message (no Bot creds): `webhook_url`, `text`.

**get_config** — Read Feishu config (AppSecret masked).

**set_config** — Update Bot config. **Requires `notify(confirm)` first.** Fields: `app_id`, `app_secret`, `webhook_url`, `self_open_id`.

---

## browser

Headless Chromium. Flow: `launch` → `navigate` → `snapshot` → actions → `close`.

**snapshot vs screenshot**: `snapshot` = aria tree for reading/interacting (zero image tokens, prefer this); `screenshot` = visual only for user — never use for LLM page analysis.

**Actions**: `launch`, `close`, `navigate`(url), `snapshot`, `screenshot`(fullPage?), `render`(html,selector?,waitSelector?,timeoutMs?), `click`(ref), `type`(ref,text), `select`(ref,values), `hover`(ref), `scroll`(ref|deltaY), `press_key`(key,ref?), `wait`(selector|timeoutMs), `evaluate`(script), `tabs`, `tab_open`(url?), `tab_close`(targetIdx), `save_cookies`(cookieName), `load_cookies`(cookieName), `list_cookies`

**render**: One-step HTML render + screenshot. Pass full HTML string; optional `selector`, `waitSelector` (default `selector + ' svg'`), `timeoutMs`. Auto-launches browser. Ideal for Mermaid — skips write→launch→navigate→wait→screenshot→close.

**Never use `navigate` + `file://`** — use `render` for local HTML; `file://` times out in Playwright sandbox.

**Key params**: `url`, `ref` (from snapshot e.g. `e3`), `text`, `key`, `values`, `selector`, `script`, `html`, `deltaY` (default 500), `fullPage`, `targetIdx`, `cookieName` (alphanumeric/-/_ only), `timeoutMs` (default 10000), `visible` (launch only), `waitSelector`

**Returns**: snapshot → `{url,title,snapshot,refCount}`; screenshot → auto-sent, don't embed URL; navigate → `{url,title,httpStatus[,needsLogin][,antiBot]}`; others → `{status,url?}`

snapshot/click/navigate clear refs — re-snapshot after. Contexts isolated per user; released after 15 min idle.

**`needsLogin`**: Stop immediately, don't retry — follow login flow once.

**Login flow**:
1. No screenshot. `notify(options)`: `"Open visible browser (local)"` / `"Guide me manually (remote)"`.
2. **Local**: `close` → `launch(visible=true)` → navigate login URL → tell user to log in and reply "continue" → wait → `close` → `launch` (headless) → navigate original URL → continue. Cookies persist automatically.
3. **Remote**: `screenshot` → guide user → wait → continue.

Anti-loop: if `needsLogin` persists after login flow, stop and tell the user.

Slider CAPTCHA: ask user for session cookie.

---

## code_search

Explore codebases. Prefer over `fs`/`exec` for reading/searching source files.

Common params: `action` (required), `path`

| action | Description | Key params |
|--------|-------------|-----------|
| `tree` | Recursive dir listing | `max_depth` (5), `include` (glob) |
| `grep` | Regex search | `pattern` (required), `include`, `max_results` (50), `context_lines` (2) |
| `glob` | Find files by pattern | `pattern` (required, `**` supported), `max_results` (300) |
| `outline` | Symbol structure, no full read | `include`; Go/Python/TS/JS/Rust/Java/Markdown |
| `chunk_read` | Read large files in segments | `chunk` (1-based, default 1), `chunk_size` (80); continue when `[K chunks remaining]` |
| `git` | Read-only git ops | `git_action` (log/blame/diff/show/status/branch/tag), `revision`, `pattern`, `n` (20) |
| `ast_grep` | AST structural search | `pattern` (`$VAR`/`$$$VARS`), `lang` (required); needs `ast-grep` |
| `comby` | Template search | `pattern` (`:[VAR]`), `include` (e.g. `.go`); needs `comby` |

---

## cron

Scheduled tasks. On trigger, system creates an isolated background session with `message` as the agent prompt.

**`message` is a natural-language agent prompt** — not a payload. E.g. `"Send Feishu message to self: <content>"`. Don't use `payload`, `description`, `task_type`, `receive_id`.

**`schedule` JSON formats**:
- `{"kind":"cron","expr":"0 9 * * *","tz":"Asia/Shanghai"}` — cron
- `{"kind":"every","everyMs":3600000}` — interval (ms)
- `{"kind":"at","at":"2026-03-20T09:00:00+08:00"}` — one-shot; auto-deleted after firing

`at` times relative to `# Current Time` in system prompt. **Must include timezone offset** (e.g. `+08:00`).

| action | Required | Description |
|--------|----------|-------------|
| `status` | — | Scheduler status + running tasks |
| `list` | — | All tasks for current user |
| `add` | `name`, `schedule`, `message` | Create task |
| `update` | `id` | Modify (name/schedule/message/enabled) |
| `remove` | `id` | Delete task |
| `run` | `id` | Trigger immediately (background) |
| `cancel` | `id` | Cancel running task → `{ok, was_running}` |
| `history` | — | Recent runs; optional `id`, `limit` (default 20, max 100) |

---

## spawn_subagent

Delegate subtask to background sub-agent. Returns `task_id` immediately; result auto-injected on completion.

| Param | Notes |
|-------|-------|
| `task` | Task prompt (required) |
| `label` | Short display name, always set (2–8 chars) |
| `context` | Background info appended to prompt |
| `notify_policy` | `done_only` (default) / `state_changes` / `silent` |
| `retain_hours` | Hours to retain (0 = ~72h default) |

Returns: `{task_id, child_session_id, status:"queued"}`

---

## cancel_subtask

Cancel queued/running sub-agent task. `force=false`: graceful, transitions to `cancelled` after current LLM call; `force=true`: immediately sets DB to `killed`.

| Param | Notes |
|-------|-------|
| `task_id` | Required |
| `reason` | Optional, saved to `error_msg` |
| `force` | Default false |

Returns: `{status:"cancelling"/"killed"/"<terminal>", note?}`

---

## report_task_progress

*(Sub-agent only)* Write progress to DB. Call after each major step. Use `notify_parent` when parent needs to respond immediately.

- `progress` (required): description string

---

## notify_parent

*(Sub-agent only)* Inject message into parent session and trigger new LLM round. Returns immediately; parent executes async. Use `report_task_progress` for routine updates.

- `message` (required)

---

## nano_banana

Generate images via nano-banana-pro. Results auto-saved and delivered — **never embed image markdown/URLs in reply**.

| action | Description | `image_urls` |
|--------|-------------|-------------|
| `txt2img` (default) | Text-to-image | Not needed |
| `img2img` | Image-to-image | Required |
| `edit` | Image editing | Required |

| Param | Notes |
|-------|-------|
| `prompt` | Description or instruction (required) |
| `image_urls` | HTTP/HTTPS URLs or server-local paths (e.g. `uploads/3/photo.jpg`) |
| `aspect_ratio` | `16:9`, `9:16`, `1:1`, `4:3`; default `16:9` |
| `size` | `2K` (default) or `4K` |

Ask for subject when vague; call directly when clear.

Config: `NANO_BANANA_API_KEY` (required), `NANO_BANANA_BASE_URL` (default `http://llmapi.bilibili.co/v1`), `NANO_BANANA_MODEL` (default `ppio/nano-banana-pro`).

---

## notify

| action | Behavior | Required | Persisted |
|--------|----------|----------|-----------|
| `progress` | Push message, continue immediately | `message` | No |
| `options` | Show option buttons, **stop and wait** | `title`, `options` | Yes |
| `confirm` | Show confirm/cancel dialog, **stop and wait** | `message` | Yes |

| Param | Notes |
|-------|-------|
| `message` | Text for progress or confirm |
| `title` | Above buttons (options only) |
| `options` | `[{"label":"...","value":"..."}, ...]` |
| `confirm_label` | Default `"Confirm"` |
| `cancel_label` | Default `"Cancel"` |

---

## skill

Unified skill operations. **Never use `fs` for skill files — always use `skill()` actions.**

**Directory hierarchy** (read priority: user-private → self-improving → system):

| Level | Path | Permission |
|-------|------|-----------|
| System | `skills/system/<skill_id>/` | Read-only for all |
| User-private | `skills/users/<userid>/<skill_id>/` | Owner r/w |
| Self-improving | `skills/users/<userid>/self-improving/skills/<skill_id>/` | Owner r/w |

**`write` sub_path**: Omit → writes `SKILL.md` (don't pass `sub_path="SKILL.md"`). After write, **must immediately call `skill(action=reload)`**.

> ⚠️ **Mandatory flow for creating a new skill — violations are rejected:**
> 1. **Read template first**: `skill(action=read_file, skill_id=skill_creator, sub_path="assets/skill_template.md")`
> 2. Fill `SKILL.md` per template (HEAD block `skill_id`, `name`, `description` all required)
> 3. `skill(action=write, skill_id=<new_id>, content=<content>)`
> 4. `skill(action=reload)`
>
> **Never call `skill(action=write)` before reading the template.**

| action | Required | Description |
|--------|----------|-------------|
| `load` | `skill_id` | Load full skill; **must call before running any skill** |
| `run_script` | `skill_id`, `script_name` | Execute script (.sh→bash, .py→python3, .js→node); 60s timeout; optional `args`, `input_json` |
| `read_script` | `skill_id`, `script_name` | Read script source only (no execution) |
| `read_file` | `skill_id`, `sub_path` | Read any file under skill root; full relative path required |
| `read_asset` | `skill_id`, `asset_name` | Read `assets/<asset_name>` |
| `read_reference` | `skill_id`, `reference_name` | Read `references/<reference_name>` |
| `write` | `skill_id`, `content` | Write to user-private dir; `skill_id`: lowercase letters/digits/underscores; **read template first for new skills** |
| `delete` | `skill_id` | Delete user-private skill + auto-reload; system skills undeletable |
| `reload` | — | Reload all skills; call immediately after write |

---

## tool_request

Report missing tool capabilities.

| action | Required | Description |
|--------|----------|-------------|
| `request` | `name`, `description` | Submit request; optional `trigger`, `input_schema`, `output_schema` |
| `list` | — | Query history; optional `status` (`pending`/`done`) |
| `close` | `id` | Mark resolved; optional `reason` |

`name` in snake_case; `description` one-line summary.

---

## output_file

Write content to file + get download URL, or get URL for existing file.

| action | Required | Returns |
|--------|----------|---------|
| `write` | `filename`, `content` | `path`, `rel_path`, `download_url`, `expires_in` |
| `download` | `file_path` | `download_url`, `expires_in` |

Use `download_url` as-is — never construct absolute URLs. Tokens expire in 30 min.

---

## fs

File system within project sandbox. Prefer `code_search` for source/doc files.

| action | Required | Description |
|--------|----------|-------------|
| `list` | `path` | List directory |
| `stat` | `path` | Metadata (size, mtime, type) |
| `read` | `path` | Read file; images → multimodal; text max 512 KB |
| `write` | `path`, `content` | Write or append (`append=true`) |
| `delete` | `path` | Delete; `recursive=true` for non-empty dirs |
| `move` | `src`, `dst` | Move/rename |
| `mkdir` | `path` | Create dir (parents auto-created) |

**Permissions**:

| Path | Read | Write |
|------|------|-------|
| `uploads/`, `output/`, `/tmp/`, `extra_fs_dirs` | ✅ | ✅ |
| `skills/users/{userID}/` | ✅ | ✅ (own user only) |
| `skills/system/` | ✅ | ❌ |
| `.env`, `*.db`, `data/` | ❌ | ❌ |

---

## mcp

Access external MCP servers. System prompt shows server summaries; fetch full schema on demand.

| action | Required | Description |
|--------|----------|-------------|
| `list` | — | All configured servers and tools |
| `detail` | `server`, `tool` | Full inputSchema for a tool |
| `call` | `server`, `tool`, `args` | Call a tool |

Call `call` directly when params are clear; `detail` first when schema is unclear. Disabled if `config/mcp.json` missing. stdio transports lazy-started. Tool lists cached per server for 5 min.

---

## kv

Session-scoped temporary storage — **cleared on session end**. Only for inter-step data within a session.

| action | Behavior |
|--------|----------|
| `get` | Return value; null if missing |
| `set` | Overwrite (any JSON type) |
| `append` | Append to array; creates if absent |

---

## memory

Cross-session persistence.

| target | Stores | actions |
|--------|--------|---------|
| `notes` | Agent notebook — env facts, tool quirks, conventions (§ separated) | get / add / replace / remove |
| `persona` | User profile — name, role, style | get / add / replace / remove |
| `user_kv` | Business data (settings, state, timestamps); non-profile → notes, non-agent-internal → persona | get / set / remove / list |

`user_kv` keys: alphanumeric + `_:.-`, max 200 chars, namespace as `"feature:attr"`. Limit: `MEMORY_SKILL_KV_ENTRY_LIMIT` (default 200); no auto-cleanup.

---

## get_session_info

Returns context identifiers. No params.

| Field | Description |
|-------|-------------|
| `user_id` | Current user (always present) |
| `session_id` | Current session (always present) |
| `session_source` | `web` or `feishu` (always present) |
| `session_title` | AI-generated title (if set) |
| `parent_session_id` | Present in continuation sessions |

For exec-based skill steps, isolate work path: `os.path.join(os.environ["AGENT_TMP_DIR"], f"{skill_id}_{session_id}")`. Scripts via `skill(action=run_script)` get `SKILL_SESSION_ID` and `SKILL_USER_ID` auto-injected.

---

## desktop (requires `DESKTOP_ENABLED=true`)

| action | Required | Description |
|--------|----------|-------------|
| `screenshot` | — | Full-screen capture; auto-delivered to user + visible to LLM |
| `get_screen_size` | — | Screen resolution |
| `mouse_move` | `x`, `y` | Move mouse |
| `left_click` | `x`, `y` | Left click |
| `right_click` | `x`, `y` | Right click |
| `double_click` | `x`, `y` | Double click |
| `type` | `text` | Type text |
| `key` | `key` | Key or combo (e.g. `ctrl+c`, `Return`, `F5`) |
| `scroll` | `x`, `y`, `direction`, `amount` (default 3) | Scroll |
| `drag` | `start_x`, `start_y`, `end_x`, `end_y` | Drag |

Flow: `screenshot` → analyze → act → `screenshot` to confirm. **Never embed image markdown/URL in reply.**

| Platform | Screenshot | Mouse/KB | Permissions |
|----------|-----------|----------|-------------|
| macOS | built-in | `brew install cliclick` | Accessibility + Screen Recording |
| Linux | `apt install scrot` | `apt install xdotool` | X11 / Xvfb |
| Windows | PowerShell | PowerShell | Run as Administrator |

---

## Usage Guidelines

1. **notify(progress)**: Push progress during waits and multi-step operations.
2. **Stop after interactive tools**: After `notify(options/confirm)`, stop and wait for user reply.
3. **notify(confirm) before irreversible actions**: delete, send, submit, etc.
4. **Skill run flow**: `skill(action=load)` → read SKILL.md → execute steps → only call `run_script`/`read_asset` when SKILL.md explicitly says so.
   **Skill create flow**: **First step must be** `skill(action=read_file, skill_id=skill_creator, sub_path="assets/skill_template.md")` → fill template → `skill(action=write)` → `skill(action=reload)`. **Never call `skill(action=write)` before reading the template.**
5. **kv vs memory(user_kv)**: `kv` = session-scoped; `memory(target=user_kv)` = cross-session persistent.
6. **skill(reload) after skill(write)**: Required — new skill won't load otherwise.
7. **tool_request**: Check list before submitting to avoid duplicates.
8. **output_file(write)**: For persisting generated files. `output_file(download)` only for existing files. Never inline large content.
9. **browser(close)**: Always close browser after task.
10. **Prefer web_fetch**: Use browser only when JS rendering is required.
11. **Reading files**: `fs(read)` for all types — .docx/.pptx/.xlsx, .pdf (`pages="1-5"` or `render=true` for scanned), images. Never use exec/Python libs to read.
12. **Writing Office/PDF**: `output_file(write, filename="xxx.docx/xlsx/pptx/pdf")`. Never use exec/Python libs.
13. **Files from exec**: Files exist only after `exec_run` returns `status:"done"`. Prefer method A (`$AGENT_OUTPUT_DIR`); fall back to `output_file(action=download, file_path=...)`.
14. **Temp file isolation**: Never hardcode `/tmp/`. exec → `$AGENT_TMP_DIR`; run_script → `tempfile.gettempdir()`. Append `{skill_id}_{session_id}` for isolation.
15. **spawn_subagent**: Use for complex/long-running/parallel tasks. After spawning, call `notify(progress)` then stop. System injects all results in batch. In reply: keep `![image](url)` as-is; `[N images auto-delivered]` — acknowledge only; `download_url` as Markdown link.
16. **Image delivery**: All image tools auto-deliver. **Never embed `![](url)`** unless `imageSentNote` explicitly requires it. To send an existing file: `output_file(action=download, file_path=...)`.
17. **matplotlib CJK fonts**: Every matplotlib script must copy the `_setup_cn_font` block from the exec section verbatim at the top (before `import matplotlib.pyplot`) and call it.
18. **SVG generation**: SVG is XML — text nodes **must escape special characters** or the browser won't render the image.

   | Char | Escape |
   |------|--------|
   | `&` | `&amp;` |
   | `<` | `&lt;` |
   | `>` | `&gt;` |
   | `"` | `&quot;` |

   **Python**: define `xe()` and wrap all variables in SVG text nodes:
   ```python
   def xe(s):
       return str(s).replace('&','&amp;').replace('<','&lt;').replace('>','&gt;').replace('"','&quot;')
   ```
   Use: `f'<text>{xe(title)}</text>'`. Static strings with `&` → write `&amp;` directly.
