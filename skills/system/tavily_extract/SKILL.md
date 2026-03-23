==============================
skill_id: tavily_extract
name: Tavily Extract
display_name: 网页内容提取
enable: true
description: 通过 Tavily CLI 从指定 URL 提取干净的 Markdown 或文本内容。支持 JavaScript 渲染页面，返回针对 LLM 优化的内容，支持查询聚焦的片段提取。单次最多处理 20 个 URL。
trigger: 当用户有一个或多个具体 URL 并想获取其内容时触发。适用于"提取内容"、"抓取这个页面"、"获取这个链接的文字"、"读取这个网页"、"extract"、"grab the content from"、"get the page at"、"read this webpage"等场景。
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
==============================

# Tavily Extract

从一个或多个 URL 提取干净的 Markdown 或文本内容。

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
# 单个 URL
tvly extract "https://example.com/article" --json

# 多个 URL
tvly extract "https://example.com/page1" "https://example.com/page2" --json

# 查询聚焦提取（只返回相关片段）
tvly extract "https://example.com/docs" --query "authentication API" --chunks-per-source 3 --json

# JavaScript 重度页面
tvly extract "https://app.example.com" --extract-depth advanced --json

# 保存到文件
tvly extract "https://example.com/article" -o article.md
```

---

## 常用参数

| 参数 | 说明 |
|------|------|
| `--query` | 按查询相关性对片段重新排序 |
| `--chunks-per-source` | 每个 URL 的片段数（1-5，需配合 `--query`） |
| `--extract-depth` | `basic`（默认）或 `advanced`（用于 JS 渲染页面） |
| `--format` | `markdown`（默认）或 `text` |
| `--include-images` | 包含图片 URL |
| `--timeout` | 最大等待时间（1-60 秒） |
| `-o, --output` | 保存到文件 |
| `--json` | 结构化 JSON 输出 |

---

## 提取深度选择

| 深度 | 适用场景 |
|------|----------|
| `basic` | 普通静态页面，速度快，优先尝试 |
| `advanced` | JS 渲染的 SPA、动态内容、包含表格的页面 |

---

## 使用技巧

- **单次最多 20 个 URL** — 超出时分批调用
- **使用 `--query` + `--chunks-per-source`** 只获取相关内容，避免全文过长
- **先试 `basic`**，内容缺失再换 `advanced`
- **慢速页面设置 `--timeout`**（最长 60s）
- 若搜索结果已通过 `--include-raw-content` 包含所需内容，可跳过 extract 步骤
