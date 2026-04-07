# OTTClaw0405

## 项目概览

Go 后端 LLM Agent 平台。核心是一个 tool-calling 循环，通过 SSE/WebSocket 向前端推流，同时支持飞书、企微、微信个人号多渠道接入。

- **语言/框架**：Go 1.25 + Gin HTTP 框架
- **数据库**：GORM + SQLite（默认）/ MySQL（生产）
- **LLM**：兼容 OpenAI Chat Completions API 的任意 provider，或 Anthropic 原生
- **前端**：`client/` 目录，纯 HTML + JS（无构建步骤）
- **浏览器自动化**：Node.js Playwright sidecar（`browser-server/server.js`）

## 目录结构

```
internal/
  agent/       — agent 主循环、子 agent 生成、后台任务
  tool/        — 所有内置工具（executor.go 是注册入口）
  storage/     — GORM 模型 + 所有 DB 操作
  handler/     — Gin HTTP 路由处理
  llm/         — LLM 客户端（openai/anthropic 两路）
  skill/       — Skill 加载与存储
  browser/     — Playwright sidecar 通信
  feishu/      — 飞书 Bot 集成
  wecom/       — 企微集成
  weixin/      — 微信个人号集成
  cron/        — 定时任务调度
  mcp/         — MCP server 懒加载注册

config/        — ROLE.md、TOOL.md、app.json、mcp.json
skills/        — system/（只读）+ users/<userid>/（读写）
scripts/       — build/deploy/start/stop 等运维脚本
browser-server/— Node.js Playwright sidecar
cmd/gen-token/ — 生成 JWT 调试 token 的小工具
```

## 常用命令

```bash
# 编译
go build ./...

# 本地运行（读取 .env）
go run main.go

# 生产启停
./scripts/start.sh
./scripts/stop.sh

# 构建二进制
./scripts/build.sh

# 打包 + 发布
./scripts/pack.sh
./scripts/publish.sh

# 生成调试 JWT token
go run cmd/gen-token/main.go
# 或
./scripts/gen-token.sh

# 启动 Playwright sidecar（browser 工具依赖它）
cd browser-server && node server.js
```

## 新增内置工具的标准流程

**必须同时改三个地方，缺一不可：**

1. **`internal/tool/executor.go` → `New()`**：注册 handler
2. **`internal/tool/executor.go` → `ToolDefinitions()`**：添加 LLM function schema
3. **`config/TOOL.md`**：如果工具比较复杂，需要补充工具文档（LLM 会读这个文件了解用法）

Feature-gated 工具（如 `desktop`、`memory`、`session_search`）在两处注册都要加 `config.Cfg.XxxEnabled` 开关，并在 `config/config.go` + `.env` + `.env.example` 同步添加配置项。

## 多模态工具结果（图片返回 LLM）

工具返回图片不走普通字符串，需用 `PartsResult` 机制：

```go
// 包装（read_image.go:61）
return NewPartsResult("文字摘要（写DB用）", []llm.ContentPart{
    {Type: "text", Text: "说明"},
    {Type: "image", MediaType: "image/jpeg", Data: base64Str},
}, "可选web路径"), nil

// agent.go 的 processToolResult 检测到 {"__cp" 前缀后自动处理，无需改动
```

大图先写临时文件，再调 `shrinkImage(absPath, origSize, config.Cfg.ReadImageMaxBytes)` 压缩（`read_image.go:156`）。`shrinkImage` 接收文件路径，**不接收 bytes**。

## Context 注入模式

tool handler 里需要的运行时依赖全部通过 `context.Value` 传入，不直接依赖 agent 包：

| 取值函数 | 用途 |
|----------|------|
| `senderFromCtx(ctx)` | 向前端推 progress |
| `interactiveSenderFromCtx(ctx)` | 推 options/confirm |
| `sessionIDFromCtx(ctx)` | 访问当前 session 的 DB 记录 |
| `userIDFromCtx(ctx)` | 当前用户 ID |
| `subagentSpawnerFromCtx(ctx)` | 启动子 agent |

## 数据库

- GORM AutoMigrate 在 `storage/db.go` 的 `InitDB()` 里统一管理
- **新增 model 字段后，必须在 `storage/db.go` 的 `AutoMigrate` 列表里同步添加**，否则字段不会建表
- 生产用 MySQL 时改 `DATABASE_DRIVER=mysql` 并填写连接参数；SQLite 文件默认在 `data/data.db`

## Skill 系统

```
skills/
  system/<skill_id>/          — 只读，所有用户共享
  users/<userid>/<skill_id>/  — 用户私有，读写
  users/<userid>/self-improving/skills/<skill_id>/ — 自进化写入路径
```

- 读取优先级：用户私有 → 自进化 → system
- **不要用 `fs` 工具直接读写 skill 文件**，用 `skill(action=load/write/...)` 保证路径解析正确
- `skill(action=write)` 后必须紧跟 `skill(action=reload)`，否则新 skill 不生效

## 配置

- 所有配置集中在 `config/AppConfig`，进程启动时加载一次（`config.Cfg` 单例）
- **修改 `.env` 后必须重启服务**，运行时不会热重载（ROLE.md 除外，通过 `update_role_md` 工具热更新）
- 优先级：系统环境变量 > `.env` 文件 > 代码默认值
- 如果有新增配置，必须在.env和.env.example中同时写入配置

## 联动修改规则
- 如果修改了config/ROLE.md，那么必须同时修改config/bootstrap/ROLE.md和skills/system/bootstrap/assets/role_template.md

## 不要做

- **不要直接修改 `config/ROLE.md`** 文件内容来改 agent 人设——用 `update_role_md` 工具，会同时更新内存缓存
- **不要在 `skills/system/` 下写文件**，该目录只读，写入会被拒绝
- **不要用 `go-cgo` 或 C 绑定**，项目保持纯 Go + shell-out
- **不要在 tool handler 里 import `internal/agent`**，依赖方向是单向的（agent → tool）
- **不要依赖工具返回空字符串作为信号**——空字符串会被 executor 自动替换为 `{"ok":true}`
- **不要改 `go.mod` 模块名 `OTTClaw`**，内部所有包都以此为前缀引用

## 常见陷阱

- 新工具只改了 `New()` 没改 `ToolDefinitions()`（或反过来）→ LLM 不认识该工具或调用报错
- `shrinkImage` 需要先把图片写到临时文件，不能传 bytes
- browser 工具报"连接拒绝" → Playwright sidecar 没启动（`cd browser-server && node server.js`）
- `LLM_CONTEXT_CACHE_ENABLED=true` 只对 Kimi/GLM/Doubao/Qwen 有效，Anthropic 用自己的 cache 机制
- SQLite 并发写冲突 → 生产建议切 MySQL（`DATABASE_DRIVER=mysql`）
- 子 agent 任务写 DB 用 `background_writer.go`，不要在子 agent 中直接调用 SSE 推流接口（无连接）
