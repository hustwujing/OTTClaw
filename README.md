# OTTClaw

> OpenClaw 的服务器版——让整个团队共享同一只 龙虾，无需每人单独部署，每个人的信息、记录相互隔离，极限节约成本，解决部门级别的龙虾使用问题。

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://golang.org)
[![Node.js](https://img.shields.io/badge/Node.js-18+-339933?logo=nodedotjs)](https://nodejs.org)
[![License](https://img.shields.io/badge/license-MIT-blue)](#license)

[OpenClaw](https://github.com/openclaw/openclaw) 是一款强大的本地 AI Agent 工具，但它只能在本地单机运行，**一套环境只能一个人用**。团队场景下，每人都要自己部署、维护配置、自己接入 LLM API、自己安装依赖——重复成本极高，尤其对非技术工种而言，更是地狱噩梦。

**OTTClaw 解决的正是这个问题。** 管理员在服务器上部署一次，通过邀请码分发访问权限；团队成员打开浏览器即可使用完整的 Agent 能力，无需安装任何本地依赖。每位成员的会话历史、浏览器登录态、生成文件**完全隔离**，互不干扰。在此基础上，进行二次开发，接入内部数据，即可达到让全员vibe办公的境界，让办公效率倍速提升。

Agent 的角色和技能通过纯文本配置文件定义（`ROLE.md` + `SKILL.md`）。当然，你不用亲自去编辑这两个文件，OTTClaw会在初始化的web页面中，通过友好的提示，让管理员能轻松快捷设置这只龙虾的角色（是打杂工？是专业编剧？），设置你团队的人要用到的公共的工作技能与流程。

---

## 功能亮点

- **OpenClaw 的团队版**：解决 OpenClaw 只能单人本地使用的限制；一台服务器部署，全团队通过浏览器访问，无需每人单独维护环境
- **邀请码访问控制**：管理员签发邀请码分发给成员，支持设备数限制和有效期；成员换取 JWT 后自动续签，无感使用
- **多用户完全隔离**：每位成员独立的会话历史、浏览器 Cookie / 登录态、KV 存储和生成文件，互不可见
- **零代码扩展**：通过 Markdown 文件定义 这只龙虾 的角色和技能，修改即生效，无需重新编译
- **Agent 循环**：LLM 自动调用工具、处理结果、持续推理，直到完成任务
- **多 LLM 支持**：OpenAI / Anthropic / 任何 OpenAI 兼容接口，支持多节点 round-robin 负载均衡
- **浏览器自动化**：稳定的浏览器操作，内置 Playwright sidecar，支持爬取、填表、截图，按用户隔离 Cookie 和登录态，让你搜集资料方便快捷
- **多平台接入**：内置 Web 界面、飞书长连接、企业微信 Webhook、Python 终端客户端
- **完整工具集**：文件系统、Shell 执行（带审批流）、KV 存储、定时任务、MCP 集成、Office 文档生成

---

## 架构

```
用户消息
  │
  ▼
┌─────────────────────────────────────────────────────┐
│                   Agent 循环                         │
│                                                     │
│  ┌─────────┐    ┌──────────────┐    ┌────────────┐  │
│  │  历史   │───▶│  LLM Client  │───▶│  工具执行  │  │
│  │  压缩   │    │(流式调用)    │◀───│  Tool Exec │  │
│  └─────────┘    └──────────────┘    └────────────┘  │
│                        │                  │         │
│                        ▼                  ▼         │
│                   纯文字回复          技能/脚本/      │
│                   → 结束循环          浏览器/KV…      │
└─────────────────────────────────────────────────────┘
  │
  ▼
流式响应（SSE / WebSocket）→ Web UI / 飞书 / 企微
```

**三个核心机制**：

| 机制 | 说明 |
|---|---|
| **技能懒加载** | 服务启动只加载技能摘要（HEAD）。LLM 调用 `skill(action=load)` 时才读取完整流程，节省 token |
| **工具文档懒加载** | 系统提示只注入工具列表，LLM 按需调用 `get_tool_doc` 获取详细参数——同一效果，消耗更少 token |
| **热更新** | `ROLE.md` / `SKILL.md` 修改后，当前对话中调用 `skill(action=reload)` 即生效，无需重启 |

---

## 快速开始

### 前置条件

| 工具 | 版本 | 用途 |
|---|---|---|
| Go | ≥ 1.21 | 编译服务端 |
| Node.js | ≥ 18 | 浏览器自动化 sidecar（可选） |
| Python | ≥ 3.8 | 控制台客户端（可选） |

### 1. 克隆与配置

```bash
git clone https://github.com/hustwujing/OTTClaw.git
cd OTTClaw

cp .env.example .env
```

编辑 `.env`，至少填写以下三项：

```env
LLM_API_KEY=sk-ant-xxxxx           # 你的 API Key
LLM_MODEL=claude-opus-4-6          # 模型名称
LLM_PROVIDER=anthropic             # openai 或 anthropic
```

> 使用 OpenAI 兼容接口时，还需设置 `LLM_BASE_URL`。

### 2. 启动服务

```bash
bash scripts/service.sh start
```

服务在后台启动，默认监听 `:8080`。

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

### 定时任务

可以设置定时任务，比如每天早上发一条消息提醒、定时抓取股市信息等。直接用对话下达指令即可。

### 绑定飞书 / 企微

跟它说"我要绑定飞书"，它会一步步引导你完成配置，之后在飞书上也能直接跟它对话。
跟它说"绑定企微机器人"或"绑定飞书群机器人"，它会引导你获取 Webhook，之后你可以让它主动往群里发消息。

### 创建自定义技能

跟它说"我要新增一个技能"，它会引导你用自然语言描述触发时机和执行流程，然后自动生成技能文件并热更新生效，无需重启。比如把你做周报总结的方法告诉它，以后它就能按你的方式帮所有人总结周报。**不同用户的自定义技能互不可见**。

> **技能的本质**：技能是用自然语言写的高层指令，底层由内置 Tool 驱动。虽然内置 Tool 不多，但组合起来足以覆盖日常大多数工作场景——代码分析、文件操作、浏览器自动化、消息推送、定时任务……大模型足够聪明，经常会超出你的预期，自己想出解决方案。

---

## 团队访问管理

OTTClaw 使用**邀请码**机制控制团队成员访问。管理员签发邀请码，成员凭码换取 JWT，之后自动续签，无需再次输入。

### 签发邀请码

```bash
# 不限设备、永不过期
go run cmd/gen-token/main.go invite alice

# 限 3 台设备、30 天有效期
go run cmd/gen-token/main.go invite alice -n 3 -ttl 30d
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
go run cmd/gen-token/main.go token alice 24h
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
    script/       ← 可执行脚本（可选）
    assets/       ← 用户资产（可选）
    references/   ← 参考资料（可选）
```

`SKILL.md` 由两行 `==============================` 分隔为 HEAD（元数据）和 CONTENT（执行流程）：

```markdown
==============================
skill_id: my_skill
name: 数据分析技能
display_name: 数据分析师
enable: true
description: 分析用户上传的数据文件，生成可视化报告
trigger: 用户说"分析数据"、"帮我看看这份表格"时触发
==============================

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

### 工具（Tool）

内置工具按用途分组，LLM 自动调用：

| 分组 | 工具 | 说明 |
|---|---|---|
| 交互 | `notify` | 向前端推送进度、选项列表、确认框 |
| 技能管理 | `skill` | 加载/创建/热更新技能 |
| 文件系统 | `fs`, `read_file`, `output_file` | 读写文件，生成 .docx/.xlsx/.pptx/.pdf |
| 脚本执行 | `exec`, `process` | 运行 Shell 命令（可配置审批流） |
| 浏览器 | `browser` | Playwright 自动化，支持 snapshot→ref→act 交互模式 |
| 消息推送 | `feishu`, `wecom` | 向飞书/企微用户或群发消息 |
| 定时任务 | `cron` | 创建/删除 cron 定时任务 |
| MCP | `mcp` | 调用外部 MCP server 工具 |
| 其他 | `kv`, `web_fetch`, `read_image`, `nano_banana` | 会话存储、抓网页、读图、生成图片 |

### 角色配置（ROLE.md）

`config/ROLE.md` 定义 AI 的身份、行为规则和语气风格，直接注入系统提示词。无需直接编辑该文件，初始化阶段系统会引导管理员以对话方式生成。

---

## 配置参考

所有配置通过 `.env` 或系统环境变量设置，优先级：系统环境变量 > `.env` > 代码默认值。

| 变量 | 默认值 | 说明 |
|---|---|---|
| `SERVER_PORT` | `8080` | 监听端口 |
| `JWT_SECRET` | _(需修改)_ | JWT 签名密钥，**生产环境必须更换** |
| `LLM_PROVIDER` | `openai` | `openai` 或 `anthropic` |
| `LLM_BASE_URL` | `https://api.openai.com` | API 基础地址 |
| `LLM_API_KEY` | _(必填)_ | API Key |
| `LLM_MODEL` | `gpt-4o` | 模型名称 |
| `LLM_RPM` | `0` | 每分钟最大请求数，0 不限制 |
| `AGENT_MAX_ITERATIONS` | `20` | Agent 单轮最大 LLM 调用次数 |
| `DATABASE_DRIVER` | `sqlite` | `sqlite` 或 `mysql` |
| `MAX_CONTEXT_TOKENS` | `6000` | 触发历史压缩的 token 估算阈值 |
| `BROWSER_HEADLESS` | `true` | 浏览器是否无头模式 |
| `FEISHU_ENCRYPT_KEY` | _(空)_ | 飞书 AppSecret 加密密钥（使用飞书集成时必填） |

多节点负载均衡：在 `.env` 中添加 `LLM_BASE_URL_2`、`LLM_API_KEY_2` 等，框架自动 round-robin。

完整配置项见 [`.env.example`](.env.example)。

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

支持登录场景：检测到登录页时，AI 弹出选项——本地运行可打开有头浏览器引导用户登录，完成后自动保存 Cookie，无头模式重启后恢复登录态。

---

## 平台集成

| 平台 | 接入方式 | 配置方式 |
|---|---|---|
| Web 界面 | 内置，访问 `:8080` | 无需配置 |
| 控制台 | `bash scripts/client.sh` | 无需配置 |
| 飞书机器人 | WebSocket 长连接，无需公网地址 | 对话中说"配置飞书机器人" |
| 企业微信 | Webhook | 对话中说"配置企业微信" |

---

## 项目结构

```
OTTClaw/
├── main.go                  # 服务入口
├── config/
│   ├── ROLE.md              # AI 角色定义（热更新）
│   ├── TOOL.md              # 工具详细说明（按需懒加载）
│   └── mcp.json             # MCP server 配置（可选）
├── skills/
│   ├── system/              # 内置系统技能
│   └── users/               # 用户自定义技能
├── internal/
│   ├── agent/               # LLM Agent 核心循环
│   ├── llm/                 # LLM 客户端（OpenAI / Anthropic）
│   ├── tool/                # 工具注册与执行
│   ├── skill/               # 技能加载与热更新
│   ├── browser/             # Playwright sidecar 管理
│   ├── handler/             # HTTP 路由（SSE / WebSocket）
│   ├── storage/             # 数据库（SQLite / MySQL）
│   ├── feishu/              # 飞书 SDK
│   ├── wecom/               # 企业微信 SDK
│   ├── cron/                # 定时任务
│   └── mcp/                 # MCP 客户端
├── browser-server/
│   └── server.js            # Node.js Playwright HTTP sidecar
├── client/
│   ├── index.html           # Web 聊天界面
│   └── client.py            # Python 控制台客户端
├── cmd/gen-token/           # 邀请码 / JWT 签发工具
└── scripts/
    ├── service.sh           # 服务管理：start / stop
    └── client.sh            # 启动控制台客户端
```

---

## License

MIT © 2026 Vijay
