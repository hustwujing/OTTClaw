---
skill_id: berserker
name: Berserker
display_name: Berserker Hive 查询
enable: true
description: Hive 表权限枚举、表结构查询、最新分区、SQL 规则检查、Hive SQL 执行、历史查看、停止任务、下载结果
trigger: 当用户提到 hive、berserker、查 hive 表、hive sql、hive 查询时触发
---

## Skill Goal

直接调用 Berserker API 执行 Hive SQL 即席查询，支持权限表枚举、SQL 规则检查、结果下载等完整查询链路。

---

## Execution Steps

### Step 1: Determine Intent

根据用户描述判断要执行的操作：

| 用户意图 | 命令 |
|---------|------|
| 查有哪些 Hive 表权限 | `tables [--keyword xxx]` |
| 查表结构 | `schema <database.table>` |
| 查最新分区 | `latest-partition <database.table>` |
| 检查 SQL 是否合规 | `check "SELECT ..."` |
| 执行 Hive SQL | `query "SELECT ..."` |
| 拉取查询结果 | `result <queryId> [--wait]` |
| 查历史记录 | `history [--days 7]` |
| 停止任务 | `stop <queryId>` |
| 下载结果 | `download <queryId>` |

### Step 2: Run Script

```
skill(action=run_script, skill_id="berserker", script_name="berserker.py",
  args=["<command>", "<arg1>", "<arg2>", ...])
```

**常用调用示例：**

```
# 列出有权限的表
args=["tables"]
args=["tables", "--keyword", "anchor"]

# 查表结构
args=["schema", "bili_main.ott_kid_tag"]

# 查最新分区
args=["latest-partition", "b_ods.ods_ott_kid_tag"]

# 执行 SQL
args=["query", "SELECT * FROM bili_main.ott_kid_tag WHERE log_date = '${yyyyMMdd}' LIMIT 100"]

# 拉结果（含等待）
args=["result", "108939163", "--wait"]

# 查询历史
args=["history", "--days", "7"]
```

### Step 3: 登录（仅在脚本输出含 `NEED_LOGIN` 时触发，最多执行 1 次）

1. `browser(action=launch, visible=false)`
2. `browser(action=navigate, url="https://berserker.bilibili.co")`
3. `browser(action=screenshot)` → **把截图 URL 嵌入回复，让用户在聊天界面看到当前页面**
4. 根据截图内容引导登录（**只能走以下三种路径之一，不得自行发挥**）：

   | 登录类型 | 操作 |
   |---------|------|
   | **已登录** | 直接跳到第 5 步 |
   | **二维码** | 展示截图让用户手机扫码；每隔 3s 执行一次 `screenshot` 直到页面跳转到主页（最多等 60s） |
   | **账号密码** | 对话向用户询问账号、密码，`type` 填入后提交 |
   | **手机验证码** | `type` 填入手机号，点击发送；对话向用户询问收到的验证码，`type` 填入后提交 |

5. `browser(action=screenshot)` 确认已进入主页
6. `browser(action=save_cookies, cookieName="berserker", urls=["https://berserker.bilibili.co"])`
7. `browser(action=close)`
8. 重新执行 Step 2

**⚠️ 严禁将 browser 工具用于执行查询、调试 API 或查看网络请求。若刷新 Cookie 后仍报错，直接将错误告知用户，不得再次触发本步骤。**

### Step 4: Display Results

- 结果行数少（≤ 20 行）：直接展示 Markdown 表格
- 结果较大：下载文件保存到 `/tmp/<userID>/berserker/` 目录，用 `read_file` 工具读取后分析

---

## 🔴 分区与时间变量规则

**用户没有明确指定日期时，MUST 使用服务端时间变量，禁止自行拼日期字符串。**

| 变量 | 含义 |
|------|------|
| `${yyyyMMdd}` | T-1 业务日期 |
| `${yyyyMMdd,-1d}` | T-2 |
| `${yyyyMMdd,first_of_week}` | 本周一 |

变量由 Berserker 服务端展开，脚本原样传递不做本地替换。分区格式为 `yyyyMMdd`（无横线）。

## 约束

- 只支持只读 SQL（SELECT/WITH/SHOW/DESC/EXPLAIN），禁止 INSERT/UPDATE/DELETE/DROP
- Cookie 由登录流程（Step 3）写入 `output/browser-cookies/<userID>/berserker.json`，长期复用
- `SKILL_USER_ID` 环境变量由系统自动注入，脚本内部直接使用，无需在 run_script 时额外传入
- **严禁通过 browser 工具操作 Berserker 网页来执行查询**。browser 工具只用于登录和保存 Cookie（Step 3），所有查询必须通过 `run_script` 调用 berserker.py 完成。`run_script` 返回 ✓ 即为成功，直接将输出展示给用户，不得再开浏览器"手操"

## ⚠️ 安全提示

C4 级数据查询需自行确保合规，所有查询会被完整记录用于审计追溯。
