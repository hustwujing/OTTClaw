==============================
skill_id: tavily_search
name: Tavily Search
display_name: 网络搜索
enable: true
description: 通过 Tavily CLI 进行网络搜索，返回针对 LLM 优化的结果，包含内容片段、相关性分数和元数据。支持域名过滤、时间范围、多种搜索深度和新闻/财经专项搜索。
trigger: 当用户想搜索网络、查找文章、获取最新资讯、发现来源，或说"搜索"、"查找"、"最新的 X 是什么"、"找一下关于"、"需要最新信息"时触发。
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
==============================

# Tavily Search

网络搜索，返回带内容片段和相关性分数的 LLM 优化结果。

---

## 前置检查

**Step 1：检查安装**

```bash
which tvly || curl -fsSL https://cli.tavily.com/install.sh | bash
```

**Step 2：检查认证，未认证则从 `.env` 加载 `TAVILY_API_KEY`**

```bash
tvly --status 2>/dev/null | grep -q "Authenticated" || {
  set -a
  source "$(git rev-parse --show-toplevel 2>/dev/null || echo '.')/.env"
  set +a
  tvly login --api-key "$TAVILY_API_KEY"
}
```

---

## 快速开始

```bash
# 基础搜索
tvly search "your query" --json

# 高级搜索，更多结果
tvly search "quantum computing" --depth advanced --max-results 10 --json

# 最新新闻
tvly search "AI news" --time-range week --topic news --json

# 限定域名
tvly search "SEC filings" --include-domains sec.gov,reuters.com --json

# 在结果中包含完整页面内容（节省单独 extract 步骤）
tvly search "react hooks tutorial" --include-raw-content --max-results 3 --json
```

---

## 常用参数

| 参数 | 说明 |
|------|------|
| `--depth` | `ultra-fast` / `fast` / `basic`（默认）/ `advanced` |
| `--max-results` | 最大结果数 0-20（默认 5） |
| `--topic` | `general`（默认）/ `news` / `finance` |
| `--time-range` | `day` / `week` / `month` / `year` |
| `--start-date` / `--end-date` | 日期范围（YYYY-MM-DD） |
| `--include-domains` | 逗号分隔的包含域名 |
| `--exclude-domains` | 逗号分隔的排除域名 |
| `--country` | 优先显示某国结果 |
| `--include-answer` | 包含 AI 摘要答案（`basic` 或 `advanced`） |
| `--include-raw-content` | 包含完整页面内容（`markdown` 或 `text`） |
| `--include-images` | 包含图片结果 |
| `--chunks-per-source` | 每个来源的片段数（仅 advanced/fast 深度） |
| `-o, --output` | 保存到文件 |
| `--json` | 结构化 JSON 输出 |

---

## 搜索深度选择

| 深度 | 速度 | 相关性 | 适用场景 |
|------|------|--------|----------|
| `ultra-fast` | 最快 | 较低 | 实时对话、自动补全 |
| `fast` | 快 | 良好 | 需要 chunks、延迟敏感 |
| `basic` | 中等 | 高 | 通用场景（默认） |
| `advanced` | 较慢 | 最高 | 精确查找、特定事实 |

---

## 使用技巧

- **查询语句保持在 400 字符以内** — 像搜索引擎关键词，不是提示词
- **复杂问题拆分为子查询** 以获得更好结果
- **使用 `--include-raw-content`** 直接在搜索结果中获取全文，避免额外 extract 调用
- **使用 `--time-range`** 获取近期信息
- 从 stdin 读取：`echo "query" | tvly search - --json`
