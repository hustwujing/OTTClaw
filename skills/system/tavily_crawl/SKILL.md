==============================
skill_id: tavily_crawl
name: Tavily Crawl
display_name: 站点批量爬取
enable: true
description: 通过 Tavily CLI 爬取网站并从多个页面提取内容。支持深度/广度控制、路径过滤、语义指令，以及将每个页面保存为本地 Markdown 文件。适用于需要批量获取文档站点内容的场景。
trigger: 当用户想爬取某个站点、下载文档、提取整个 docs 章节、批量提取页面、将站点保存为本地 Markdown 文件，或说"crawl"、"get all the pages"、"download the docs"、"extract everything under /docs"、"bulk extract"、"爬取"、"下载文档"、"批量抓取"时触发。
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
==============================

# Tavily Crawl

爬取网站并从多个页面提取内容，支持将每个页面保存为本地 Markdown 文件。

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
# 基础爬取
tvly crawl "https://docs.example.com" --json

# 将每个页面保存为 Markdown 文件
tvly crawl "https://docs.example.com" --output-dir ./docs/

# 更深层爬取，带限制
tvly crawl "https://docs.example.com" --max-depth 2 --limit 50 --json

# 路径过滤
tvly crawl "https://example.com" --select-paths "/api/.*,/guides/.*" --exclude-paths "/blog/.*" --json

# 语义聚焦（返回相关片段而非完整页面）
tvly crawl "https://docs.example.com" --instructions "Find authentication docs" --chunks-per-source 3 --json
```

---

## 常用参数

| 参数 | 说明 |
|------|------|
| `--max-depth` | 爬取深度（1-5，默认 1） |
| `--max-breadth` | 每页链接数（默认 20） |
| `--limit` | 最大页面数上限（默认 50） |
| `--instructions` | 自然语言指令，用于语义聚焦 |
| `--chunks-per-source` | 每页片段数（1-5，需配合 `--instructions`） |
| `--extract-depth` | `basic`（默认）或 `advanced` |
| `--format` | `markdown`（默认）或 `text` |
| `--select-paths` | 逗号分隔的路径正则，包含匹配路径 |
| `--exclude-paths` | 逗号分隔的路径正则，排除匹配路径 |
| `--select-domains` | 包含的域名正则 |
| `--exclude-domains` | 排除的域名正则 |
| `--allow-external / --no-external` | 是否包含外部链接（默认允许） |
| `--include-images` | 包含图片 |
| `--timeout` | 最大等待时间（10-150 秒） |
| `-o, --output` | 保存 JSON 输出到文件 |
| `--output-dir` | 将每个页面保存为 .md 文件到目录 |
| `--json` | 结构化 JSON 输出 |

---

## 两种使用模式

**agentic 模式**（将结果传给 LLM）：

使用 `--instructions` + `--chunks-per-source`，只返回相关片段，避免 context 爆炸。

```bash
tvly crawl "https://docs.example.com" --instructions "API authentication" --chunks-per-source 3 --json
```

**数据收集模式**（保存到文件）：

使用 `--output-dir` 不带 `--chunks-per-source`，获取完整页面的 Markdown 文件。

```bash
tvly crawl "https://docs.example.com" --max-depth 2 --output-dir ./docs/
```

---

## 使用技巧

- **从保守参数开始** — `--max-depth 1`、`--limit 20`，按需扩大
- **使用 `--select-paths`** 聚焦到目标章节
- **爬取前先用 map** 了解站点结构
- **始终设置 `--limit`** 防止失控爬取
