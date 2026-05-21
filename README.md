# OTTClaw

> OpenClaw 的服务器版——让整个团队共享同一只 龙虾，无需每人单独部署，每个人的信息、记录相互隔离，极限节约成本，解决部门级别的龙虾使用问题。

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://golang.org)
[![Node.js](https://img.shields.io/badge/Node.js-18+-339933?logo=nodedotjs)](https://nodejs.org)
[![License](https://img.shields.io/badge/license-MIT-blue)](#license)

[OpenClaw](https://github.com/openclaw/openclaw) 是一款强大的本地 AI Agent 工具，但它只能在本地单机运行，**一套环境只能一个人用**。团队场景下，每人都要自己部署、维护配置、自己接入 LLM API、自己安装依赖——重复成本极高，尤其对非技术工种而言，更是地狱噩梦。

**OTTClaw 解决的正是这个问题。** 管理员在服务器上部署一次，通过邀请码分发访问权限；团队成员打开浏览器即可使用完整的 Agent 能力，无需安装任何本地依赖。每位成员的会话历史、浏览器登录态、生成文件**完全隔离**，互不干扰。在此基础上，进行二次开发，接入内部数据，即可达到让全员vibe办公的境界，让办公效率倍速提升。如果你正在愁，怎么为全部门的人能方便快捷地使用龙虾而发愁，那么本项目就是你正在寻找的方案。

Agent 的角色和技能通过纯文本配置文件定义（`ROLE.md` + `SKILL.md`）。当然，你不用亲自去编辑这两个文件，OTTClaw会在初始化的web页面中，通过友好的提示，让管理员能轻松快捷设置这只龙虾的角色（是打杂工？是专业编剧？），设置你团队的人要用到的公共的工作技能与流程。

---

## 功能亮点

- **OpenClaw 的团队版**：解决 OpenClaw 只能单人本地使用的限制；一台服务器部署，全团队通过浏览器访问，无需每人单独维护环境
- **邀请码访问控制**：管理员签发邀请码分发给成员，支持设备数限制和有效期；成员换取 JWT 后自动续签，无感使用
- **多用户完全隔离**：每位成员独立的会话历史、浏览器 Cookie / 登录态、KV 存储和生成文件，互不可见
- **零代码扩展**：通过 Markdown 文件定义 这只龙虾 的角色和技能，修改即生效，无需重新编译
- **Agent 循环**：LLM 自动调用工具、处理结果、持续推理，直到完成任务
- **并行子 Agent**：主 Agent 可将复杂任务自动拆解为多个子 Agent 并行执行，子任务完成后自动汇总；前端实时显示每个子任务的进度卡片，结果通过 SSE 推送
- **多 LLM 支持**：OpenAI / Anthropic / 任何 OpenAI 兼容接口，支持多节点 round-robin 负载均衡
- **浏览器自动化**：稳定的浏览器操作，内置 Playwright sidecar，支持爬取、填表、截图，按用户隔离 Cookie 和登录态
- **桌面控制**：可选接入本机桌面（`DESKTOP_ENABLED=true`），截图、移动鼠标、点击、键入、滚动、拖拽，跨平台纯 shell-out 实现（macOS / Linux / Windows），与浏览器工具配合完成 GUI 自动化流程
- **长期记忆**：跨会话持久化 Agent 笔记（环境约定、用户偏好）与用户人设；另有全员共享的系统级笔记（`system_notes`），适合写入工具特性、环境约定等全局知识；内置后台 flush/review 机制，配合跨会话全文搜索召回历史上下文
- **自我进化技能**：Agent 自动将高频重复操作提炼为技能，近似 LFU 淘汰冷门技能，持续优化工作流程
- **定时任务**：支持 cron 表达式、固定间隔、单次定时三种触发方式；Web 界面可查看执行历史，支持实时取消/强制中止/立即触发
- **多平台接入**：内置 Web 界面、飞书长连接、企业微信 Webhook、微信个人号、Python 终端客户端
- **完整工具集**：文件系统、Shell 执行（含 AST 级静态安全分析，自动拦截反弹 Shell / curl|sh 等危险模式）、代码检索（tree/glob/grep/AST/comby）、KV 存储、定时任务、MCP 集成、Office 文档生成
- **可观测性（Langfuse）**：可选接入 Langfuse，自动上报 Agent trace、LLM generation、工具 span，并对每段对话异步评估任务完成度（Task Unit Score），全链路可观测

---

## 架构

```
用户消息
  │
  ▼
┌──────────────────────────────────────────────────────────┐
│                      Agent 循环                           │
│                                                          │
│  ┌─────────┐    ┌──────────────┐    ┌────────────────┐   │
│  │  历史   │───▶│  LLM Client  │───▶│   工具执行     │   │
│  │  压缩   │    │ (流式调用)   │◀───│   Tool Exec    │   │
│  └─────────┘    └──────────────┘    └────────────────┘   │
│                        │                    │            │
│                        │            spawn_subagent       │
│                        │                    │            │
│                        │       ┌────────────┼──────┐     │
│                        │       ▼            ▼      ▼     │
│                        │   [子Agent1]  [子Agent2]  …     │
│                        │       └────────────┼──────┘     │
│                        │            汇总结果 →  父Agent  │
│                        │                                  │
│                        ▼                                  │
│                   纯文字回复                              │
│                   → 结束循环                              │
│                                                          │
│  ┌──────────────────────────────────────────────────┐    │
│  │                  长期记忆层                        │    │
│  │  notes/persona 写入  ·  session_search 召回       │    │
│  │  后台 flush/review   ·  自我进化技能提炼           │    │
│  └──────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────┘
  │
  ▼
流式响应（SSE / WebSocket）→ Web UI / 飞书 / 企微
```

定时任务（cron）触发时，在后台独立 session 中运行 Agent，结果通过 SSE 实时推送到 Web 界面。

---

## 快速开始

### 前置条件

| 工具 | 版本 | 用途 |
|---|---|---|
| Go | ≥ 1.25 | 编译服务端 |
| Node.js | ≥ 18 | 浏览器自动化 sidecar（可选） |
| Python | ≥ 3.8 | 控制台客户端（可选） |

### 1. 克隆与配置

```bash
git clone https://github.com/hustwujing/OTTClaw.git
cd OTTClaw

cp .env.example .env
```

编辑 `.env`，至少填写以下信息：

```env
LLM_API_KEY=                              # 你的 API Key
LLM_MODEL=                                 # 模型名称
LLM_PROVIDER=anthropic/openai             # openai 或 anthropic
LLM_BASE_URL=
```

### 2. 启动服务

```bash
bash scripts/service.sh start
```

服务在后台启动，默认监听 `:8081`。

```bash
tail -f logs/stdout.log    # 查看运行日志
bash scripts/service.sh stop   # 停止服务
```

### 3. 访问 Web 界面

打开 `http://localhost:8081`，在"连接配置"弹窗中输入邀请码完成登录，即可开始对话。

### 4. 控制台客户端（可选）

```bash
bash scripts/client.sh
```

脚本自动创建 Python 虚拟环境、安装依赖，启动终端交互界面。

---

## 功能一览

如果你用过 OpenClaw，那告诉你：OpenClaw 有的它都有，而且可以多人同时用。你只要下达任务，它会想方设法帮你完成。下面是一些典型能力：

### 代码分析

直接上传一个压缩包，它会自动解压，然后分析里面的代码，回答你关于这个工程的任何问题。作为部门的 AI 入口，管理员可以创建一个代码解析的技能，定期把 Git 代码同步到服务器某个目录，开放给全员咨询。

### 图形能力

接入了图像生成模型（`nano_banana`），支持文生图、图生图、修图。你也可以开发工具接入其他图像模型。

### 写文档 / 写 PPT

内置 `.docx`、`.pptx` 生成能力。实际上，即便不提供专用工具，它也会自己写 Python 脚本来完成任务——比如让它给你画上海今日天气折线图，它会自己上网抓数据、写脚本、把图渲染出来给你。

### 操作浏览器

它可以代你上网搜集资料、填表、截图。遇到验证码会停下来问你；遇到扫码登录会把二维码发给你；遇到滑块验证会尝试自动绕过。

### 控制桌面

启用 `DESKTOP_ENABLED=true` 后，它可以直接控制你的机器：截图、移动鼠标、点击、键入文字、按快捷键、滚动页面、拖拽元素。典型用法是用截图"看"当前屏幕状态，然后决定下一步操作，像人一样完成 GUI 任务——即便没有浏览器也能工作（操作桌面应用、IDE、终端等）。

### 并行子任务

对于复杂任务，它会自动拆分成多个子任务，派发给多个独立子 Agent 并行执行。你在聊天界面可以实时看到每个子任务的进度卡片（正在进行 / 已完成 / 失败），父 Agent 在全部子任务完成后自动汇总结果推送给你——无需等待、无需刷新。

### 定时任务

可以设置定时任务，比如每天早上发一条消息提醒、定时抓取股市信息等。直接用对话下达指令即可。Web 界面"定时任务历史"面板可查看所有执行记录，支持搜索、分页，并可对运行中的任务执行取消 / 强制中止，或随时手动触发。

### 绑定飞书 / 企微 / 微信

跟它说"我要绑定飞书"，它会一步步引导你完成配置，之后在飞书上也能直接跟它对话。
跟它说"绑定企微机器人"或"绑定飞书群机器人"，它会引导你获取 Webhook，之后你可以让它主动往群里发消息。
微信个人号同样支持：跟它说"绑定微信"，它会弹出二维码，扫码后即可在微信里收发消息（需在 `.env` 中提前设置 `WEIXIN_ENCRYPT_KEY`）。

### 创建自定义技能

跟它说"我要新增一个技能"，它会引导你用自然语言描述触发时机和执行流程，然后自动生成技能文件并热更新生效，无需重启。比如把你做周报总结的方法告诉它，以后它就能按你的方式帮所有人总结周报。**不同用户的自定义技能互不可见**。

> **技能的本质**：技能是用自然语言写的高层指令，底层由内置 Tool 驱动。虽然内置 Tool 不多，但组合起来足以覆盖日常大多数工作场景——代码分析、文件操作、浏览器自动化、消息推送、定时任务……大模型足够聪明，经常会超出你的预期，自己想出解决方案。

---

## 团队访问管理

OTTClaw 使用**邀请码**机制控制团队成员访问。管理员签发邀请码，成员凭码换取 JWT，之后自动续签，无需再次输入。

### 签发邀请码

```bash
# 不限设备、永不过期
bash scripts/gen-token.sh invite alice

# 限 3 台设备、30 天有效期
bash scripts/gen-token.sh invite alice -n 3 -ttl 30d
```

输出示例：

```
账号名  : alice
邀请码  : ABCD-EF23-GH45-JK67
设备限制: 3 台
有效期至: 2026-04-20 10:00:00
```

将邀请码发送给团队成员，成员在 Web 界面的"连接配置"弹窗中填入即可。每台设备激活一次后绑定设备指纹，不会重复占用名额。

### 参数说明

| 参数 | 说明 | 示例 |
|---|---|---|
| `alice` | 账号名，仅用于标识，不影响功能 | 姓名、工号均可 |
| `-n 3` | 最多绑定 3 台设备，超出后邀请码失效 | 省略则不限设备 |
| `-ttl 30d` | 邀请码有效期，支持 `7d`、`24h` 等格式 | 省略则永不过期 |

### 调试用直签 JWT

本地开发或控制台客户端无需邀请码，可直接签发 JWT：

```bash
bash scripts/gen-token.sh token alice 24h
```

> 控制台客户端会自动从本地 `.env` 读取 `JWT_SECRET` 完成签发，无需手动操作。

### 安全建议

- **生产环境必须修改 `JWT_SECRET`**：在 `.env` 中设置为随机长字符串
  ```bash
  openssl rand -hex 32   # 生成示例
  ```
- 邀请码一旦泄露可立即废止（数据库删除对应记录）
- 通过 `-ttl` 设置有效期可降低泄露风险

---

## 核心概念

### 技能（Skill）

每个技能是 `skills/` 下的一个目录，包含一个 `SKILL.md`：

```
skills/
  ${user_name}/
    SKILL.md      ← 技能定义
    scripts/      ← 可执行脚本（可选）
    assets/       ← 用户资产（可选）
    references/   ← 参考资料（可选）
```

`SKILL.md` 使用标准 YAML Front Matter 格式，由 `---` 分隔为 HEAD（元数据）和 CONTENT（执行流程）：

```markdown
---
skill_id: my_skill
name: 数据分析技能
display_name: 数据分析师
enable: true
description: 分析用户上传的数据文件，生成可视化报告
trigger: 用户说"分析数据"、"帮我看看这份表格"时触发
---

## 执行流程

### 第一步：读取数据
调用 `read_file` 工具读取用户上传的文件...

### 第二步：分析并生成报告
...
```

LLM 读取 CONTENT 后，按自然语言流程描述自主完成任务。**无需写代码**。

通过 AI 对话创建新技能：

```
你：我想新增一个每日邮件摘要技能
AI：好的，先告诉我触发时机和执行流程…（引导完成后热更新生效）
```

### 角色配置（ROLE.md）

`config/ROLE.md` 定义 AI 的身份、行为规则和语气风格，直接注入系统提示词。无需直接编辑该文件，初始化阶段系统会引导管理员以对话方式生成。

### 长期记忆

Agent 拥有三层跨会话持久记忆：

- **notes**：Agent 自身的环境笔记（per-user），用 `§` 分隔条目，记录工具特性、环境约定、稳定规律等
- **persona**：用户人设（per-user），记录用户姓名、角色、偏好、沟通风格等自由文本
- **system_notes**：全员共享的系统级笔记（管理员 / Agent 写入），适合写入整个平台的工具约束、环境特性等全局知识，所有用户的会话均可见

每次会话结束后，Agent 会在后台自动 flush 新知识到记忆；每隔 N 轮还会触发 review，清理过时条目。所有写入前均进行安全扫描，拦截不可见 Unicode 字符和 prompt injection 尝试。

**跨会话搜索（`session_search`）**：Agent 可通过 FTS5 全文检索历史会话，以关键词为锚点居中截取上下文窗口，调用辅助 LLM 生成摘要，召回当前 context window 之外的历史信息。无查询词时直接返回近期会话元数据，零额外 LLM 开销。

### 自我进化技能

Agent 在完成高频重复操作后，会自动将操作流程提炼为技能文件，写入 `self-improving/skills/` 目录，热更新后下次直接复用，无需重新推理。

技能库按近似 LFU（最近最少使用）策略管理容量：
- 每次加载技能时更新使用计数，计数器按配置的半衰期自动衰减
- 超出上限时淘汰得分最低的技能
- 新技能有保护窗口，窗口期内不参与淘汰

所有自进化写入同样经过安全扫描：SKILL.md 检查 prompt injection，`scripts/` 文件额外检查反弹 Shell / base64+exec / curl|sh 等危险命令模式。

### 并行子 Agent

主 Agent 通过 `spawn_subagent` 工具将子任务派发给独立后台 Agent。每个子 Agent 运行在独立 session 中，完成后将结果回写到父会话。所有子任务完成后，父 Agent 自动被唤醒进行汇总推理，结果通过 SSE 实时推送到 Web 界面。

进程重启时，系统自动将上次卡住的 `queued`/`running` 子任务标记为失败并通知父 Agent（孤儿恢复机制），防止任务无限挂起。

---

## 配置参考

所有配置通过 `.env` 或系统环境变量设置，优先级：系统环境变量 > `.env` > 代码默认值。完整配置项见 [`.env.example`](.env.example)。

### 核心配置

| 变量 | 默认值 | 说明 |
|---|---|---|
| `SERVER_PORT` | `8081` | 监听端口 |
| `JWT_SECRET` | _(需修改)_ | JWT 签名密钥，**生产环境必须更换** |
| `LLM_PROVIDER` | `openai` | `openai` 或 `anthropic` |
| `LLM_BASE_URL` | `https://api.openai.com` | API 基础地址 |
| `LLM_API_KEY` | _(必填)_ | API Key |
| `LLM_MODEL` | `gpt-4o` | 模型名称 |
| `LLM_MAX_TOKENS` | `8096` | 最大输出 token 数（Anthropic 必填） |
| `LLM_RPM` | `0` | 每分钟最大请求数，0 不限制 |
| `AGENT_MAX_ITERATIONS` | `20` | Agent 单轮最大 LLM 调用次数 |
| `DATABASE_DRIVER` | `sqlite` | `sqlite` 或 `mysql` |
| `DATABASE_PATH` | `data/data.db` | SQLite 文件路径 |
| `MAX_CONTEXT_TOKENS` | `20000` | 触发历史压缩的 token 估算阈值 |
| `COMPRESS_KEEP_RECENT` | `10` | 压缩时保留最新的 N 条消息不参与摘要 |
| `SUBTASK_RETENTION_DAYS` | `7` | 终态子任务记录保留天数，超出后自动删除；0 禁用 |

多节点负载均衡：在 `.env` 中添加 `LLM_BASE_URL_2`、`LLM_API_KEY_2`、`LLM_MODEL_2` 等，框架自动 round-robin。最多支持 10 个节点。

### 长期记忆

| 变量 | 默认值 | 说明 |
|---|---|---|
| `MEMORY_ENABLED` | `true` | 是否启用 memory 工具；`false` 时工具不暴露给 LLM |
| `MEMORY_FLUSH_MIN_TURNS` | `6` | 触发会话结束 flush 所需最少 user 消息数，0 禁用 |
| `MEMORY_NUDGE_INTERVAL` | `10` | 后台 review 触发轮次间隔，0 禁用 |
| `MEMORY_NOTES_CHAR_LIMIT` | `2200` | Agent notes 字符上限 |
| `MEMORY_PERSONA_CHAR_LIMIT` | `1375` | 用户人设字符上限 |
| `MEMORY_SKILL_KV_ENTRY_LIMIT` | `200` | user_kv 每用户最大条目数 |
| `SYSTEM_NOTES_CHAR_LIMIT` | `1024` | 系统级笔记（全员共享）字符上限 |
| `SESSION_SEARCH_ENABLED` | `true` | 是否启用跨会话全文搜索工具 |
| `SESSION_SEARCH_SUMMARY_MAX_CHARS` | `50000` | 摘要上下文窗口最大字符数（居中截断） |

### 自我进化技能

| 变量 | 默认值 | 说明 |
|---|---|---|
| `SELF_IMPROVING_MIN_TOOL_ITERS` | `3` | 触发自我进化所需最少工具调用轮次，0 禁用 |
| `SELF_IMPROVING_MAX_SKILLS` | `20` | 每用户自进化技能上限，超出按 LFU 淘汰，0 禁用淘汰 |
| `SELF_IMPROVING_LFU_DECAY_HOURS` | `24` | LFU 计数器半衰期（小时），越小衰减越快 |
| `SELF_IMPROVING_PROTECT_MINUTES` | `60` | 新技能保护窗口（分钟），窗口内不参与淘汰，0 禁用 |

### 浏览器自动化

| 变量 | 默认值 | 说明 |
|---|---|---|
| `BROWSER_HEADLESS` | `true` | 是否无头模式；调试时设 `false` 可看到浏览器窗口 |
| `BROWSER_SERVER_PORT` | `9222` | Node.js Playwright sidecar 监听端口 |
| `BROWSER_USER_DATA_BASE` | `data/browser-profiles` | per-user Chrome Profile 根目录 |

### 飞书集成

| 变量 | 默认值 | 说明 |
|---|---|---|
| `FEISHU_ENCRYPT_KEY` | _(空)_ | AppSecret 加密密钥（使用飞书集成时必填） |
| `FEISHU_API_BASE` | `https://open.feishu.cn` | 飞书 Open API 基础地址，私有部署时修改 |
| `FEISHU_SPINNER_INTERVAL_MS` | `800` | 飞书侧 spinner 刷新间隔（毫秒） |
| `FEISHU_PENDING_TIMEOUT_MIN` | `30` | 等待用户操作（上传文件等）超时分钟数 |

### 微信个人号集成

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WEIXIN_ENCRYPT_KEY` | _(空)_ | AES-GCM 加密用户微信 token 的服务端密钥（绑定微信前必须设置） |

### 工具行为

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DESKTOP_ENABLED` | `false` | 是否启用桌面控制工具（截图 / 鼠标 / 键盘），仅在有物理/虚拟桌面的机器上启用 |
| `TOOL_SCRIPT_TIMEOUT_SEC` | `60` | run_script 工具执行超时（秒） |
| `TOOL_EXEC_TIMEOUT_SEC` | `1800` | exec 工具总超时（秒，30 分钟） |
| `TOOL_EXEC_YIELD_MS` | `10000` | exec 工具默认 yield 等待时间（毫秒） |
| `TOOL_WEB_FETCH_TIMEOUT_SEC` | `15` | web_fetch HTTP 请求超时（秒） |
| `TOOL_RESULT_MAX_DB_BYTES` | `2000` | 工具结果写入 DB 的最大字节数，超出截断 |
| `DOWNLOAD_TTL_MIN` | `30` | output_file 生成的下载链接有效期（分钟） |
| `FS_READ_MAX_BYTES` | `524288` | fs read 允许读取的最大字节数（512 KB） |
| `READ_FILE_MAX_BYTES` | `20971520` | read_file 从 .docx/.pdf/.pptx 提取文本的最大字节数（20 MB） |
| `UPLOAD_MAX_BYTES` | `20971520` | 单次上传文件大小上限（20 MB） |
| `MCP_CONFIG_PATH` | `config/mcp.json` | MCP server 配置文件路径 |

### Langfuse 可观测性（可选）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `LANGFUSE_ENABLED` | `false` | 是否启用 Langfuse 上报 |
| `LANGFUSE_BASE_URL` | `http://localhost:3000` | Langfuse 服务地址（自托管或云端） |
| `LANGFUSE_PUBLIC_KEY` | _(空)_ | Langfuse Project Public Key |
| `LANGFUSE_SECRET_KEY` | _(空)_ | Langfuse Project Secret Key |
| `LANGFUSE_SCORER_IDLE_MINUTES` | `15` | 会话静默多少分钟后触发任务完成度评估 |
| `LANGFUSE_SCORER_MAX_WINDOW` | `20` | 单次评估最多取最近 N 条消息 |

启用后，每次 Agent 运行自动上报：顶层 `trace`（含 session/user/input）、`generation`（LLM 调用、token 用量）、`span`（每个工具调用耗时）；后台异步对每段对话评估任务完成度，score 挂载到对应 trace。

### 外部服务（可选）

| 变量 | 说明 |
|---|---|
| `NANO_BANANA_API_KEY` | 图像生成 API Key |
| `FIRECRAWL_API_KEY` | 反爬回退（summarize CLI） |
| `APIFY_API_TOKEN` | YouTube 字幕提取备用 |
| `HONCHO_ENABLED` | 启用 Honcho AI-native memory 平台集成 |
| `HONCHO_API_KEY` | Honcho API Key |

---

## 浏览器自动化

服务启动时自动拉起 Node.js Playwright sidecar，每个用户有独立的 BrowserContext（隔离 Cookie），空闲 15 分钟自动清理。

**安装依赖（首次使用）：**

```bash
cd browser-server && npm install
```

**snapshot → ref → act 交互模式：**

```
launch → navigate → snapshot（获取带 ref 的元素树）→ click/type（按 ref 操作）
```

支持登录场景：`navigate` / `snapshot` 自动检测登录墙（HTTP 401、标题含"需要登录"等特征词），返回 `needsLogin` 字段后 AI 立即停止重试，弹出选项引导用户登录——本地运行可打开有头浏览器完成登录，完成后按域名过滤保存 Cookie，无头模式重启后恢复登录态。

同样自动检测 WAF / Cloudflare 拦截（HTTP 403/429/503、标题含反爬特征词），返回 `antiBot` 字段提示。

---

## 平台集成

| 平台 | 接入方式 | 配置方式 |
|---|---|---|
| Web 界面 | 内置，访问 `:8081` | 无需配置 |
| 控制台 | `bash scripts/client.sh` | 无需配置 |
| 飞书机器人 | WebSocket 长连接，无需公网地址 | 对话中说"配置飞书机器人" |
| 企业微信 | Webhook | 对话中说"配置企业微信" |
| 微信个人号 | 扫码绑定，收发消息 | 设置 `WEIXIN_ENCRYPT_KEY`，对话中说"绑定微信" |

---

## 项目结构

```
OTTClaw/
├── main.go                  # 服务入口
├── config/
│   ├── config.go            # 全局配置
│   ├── ROLE.md              # AI 角色定义（热更新）
│   ├── TOOL.md              # 工具详细说明（按需懒加载）
│   ├── mcp.json             # MCP server 配置（可选）
│   └── bootstrap/           # 初始化引导模板（ROLE.md 默认内容等）
├── skills/
│   ├── system/              # 内置系统技能
│   └── users/               # 用户自定义技能
│       └── {user}/
│           └── self-improving/skills/  # 自我进化技能
├── internal/
│   ├── agent/               # LLM Agent 核心循环
│   │   ├── agent.go         # Agent 主循环、工具调度、自我进化
│   │   ├── runner.go        # 子 Agent 调度：派发、批量通知、汇总
│   │   ├── spawn_cmd.go     # /subagents spawn 命令解析
│   │   ├── cron_runner.go   # 定时任务 Agent 执行封装
│   │   ├── orphan_recovery.go   # 孤儿子任务恢复（进程重启后修复卡住任务）
│   │   ├── subtask_gc.go        # 过期子任务定期清理
│   │   ├── session_scorer.go    # Langfuse Task Unit Score 异步评估
│   │   ├── titler.go            # 会话自动命名
│   │   ├── background_writer.go # 后台静默写入器
│   │   └── subagent_writer.go   # 子 Agent SSE 推送写入器
│   ├── llm/                 # LLM 客户端（OpenAI / Anthropic）
│   ├── tool/                # 工具注册与执行（含 memory / session_search / desktop / code_search / exec_safety）
│   ├── skill/               # 技能加载、热更新、LFU 管理、安全扫描
│   ├── browser/             # Playwright sidecar 管理（含 manager_unix.go / manager_windows.go 跨平台实现）
│   ├── langfuse/            # Langfuse 可观测性客户端（异步批量上报）
│   ├── handler/             # HTTP 路由
│   │   ├── sse.go           # SSE 流式对话
│   │   ├── ws.go            # WebSocket 对话
│   │   ├── cron.go          # 定时任务 REST API
│   │   ├── subtask.go       # 子任务操作 API
│   │   ├── stats.go         # 并发统计 API
│   │   └── notify.go        # SSE 主动推送（cron/子任务结果）
│   ├── storage/             # 数据库（SQLite / MySQL）+ FTS5 全文索引
│   ├── feishu/              # 飞书 SDK
│   ├── wecom/               # 企业微信 SDK
│   ├── cron/                # 定时任务调度器
│   ├── push/                # 服务端推送注册表（pub-sub）
│   ├── runtrack/            # 并发任务运行追踪
│   └── mcp/                 # MCP 客户端
├── browser-server/
│   └── server.js            # Node.js Playwright HTTP sidecar
├── client/
│   ├── index.html           # Web 聊天界面
│   └── client.py            # Python 控制台客户端
├── cmd/gen-token/           # 邀请码 / JWT 签发工具
├── cmd/migrate-scorer/      # 历史 session 评分迁移工具（一次性）
└── scripts/
    ├── service.sh           # 服务管理：start / stop（含端口清理、自动编译）
    ├── gen-token.sh         # 签发邀请码 / JWT
    ├── build.sh             # 仅编译二进制
    ├── deploy.sh            # 打包 + 上传到远端服务器
    ├── pack.sh              # 打包发行 zip
    └── client.sh            # 启动 Python 控制台客户端
```

---

## License

MIT © 2026 Vijay
