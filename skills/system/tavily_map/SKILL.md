==============================
skill_id: tavily_map
name: Tavily Map
display_name: 站点 URL 发现
enable: true
description: 通过 Tavily CLI 发现和列出网站上的所有 URL，不提取内容。比爬取更快，用于了解站点结构、定位特定页面，或在决定爬取前先探索域名。
trigger: 当用户想在大型站点上查找特定页面、列出所有 URL、了解站点结构、查找某个域名上的内容，或说"map the site"、"find the URL for"、"what pages are on"、"list all pages"、"site structure"、"发现 URL"、"站点地图"时触发。
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
==============================

# Tavily Map

发现网站上的 URL，不提取内容。比爬取更快，用于定位目标页面。

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
# 发现所有 URL
tvly map "https://docs.example.com" --json

# 自然语言过滤
tvly map "https://docs.example.com" --instructions "Find API docs and guides" --json

# 按路径过滤
tvly map "https://example.com" --select-paths "/blog/.*" --limit 500 --json

# 深度 map
tvly map "https://example.com" --max-depth 3 --limit 200 --json
```

---

## 常用参数

| 参数 | 说明 |
|------|------|
| `--max-depth` | 爬取深度（1-5，默认 1） |
| `--max-breadth` | 每页链接数（默认 20） |
| `--limit` | 最大发现 URL 数（默认 50） |
| `--instructions` | 自然语言 URL 过滤指令 |
| `--select-paths` | 逗号分隔的路径正则，包含匹配路径 |
| `--exclude-paths` | 逗号分隔的路径正则，排除匹配路径 |
| `--select-domains` | 包含的域名正则 |
| `--exclude-domains` | 排除的域名正则 |
| `--allow-external / --no-external` | 是否包含外部链接 |
| `--timeout` | 最大等待时间（10-150 秒） |
| `-o, --output` | 保存到文件 |
| `--json` | 结构化 JSON 输出 |

---

## Map + Extract 组合模式

先用 map 定位目标页面，再用 extract 提取内容。比全站爬取更高效：

```bash
# 第一步：找到认证文档的 URL
tvly map "https://docs.example.com" --instructions "authentication" --json

# 第二步：提取找到的具体页面
tvly extract "https://docs.example.com/api/authentication" --json
```

---

## 使用技巧

- **Map 只做 URL 发现** — 不提取内容，需要内容请用 `extract` 或 `crawl`
- **Map + extract 优于 crawl** — 当只需要大型站点中少数几个页面时
- **使用 `--instructions`** 在路径模式不够用时进行语义过滤
