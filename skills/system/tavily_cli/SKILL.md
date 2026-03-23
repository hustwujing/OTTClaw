==============================
skill_id: tavily_cli
name: Tavily CLI
display_name: 网络搜索与研究
enable: true
description: 通过 Tavily CLI (tvly) 进行网络搜索、内容提取、站点爬取、URL 发现和 AI 深度研究。适用于需要实时网络数据、抓取页面内容、爬取文档站点或进行多源综合分析的场景。
trigger: 当用户想要搜索网络、查找文章、研究某个话题、提取 URL 内容、抓取网页文字、爬取文档、下载站点页面、发现域名下的 URL，或进行带引用的深度研究时触发。也适用于"fetch this page"、"pull the content from"、"get the page at https://"、"find me articles about"等表述。
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
==============================

# Tavily CLI

通过 `tvly` 命令行工具进行网络搜索、内容提取、站点爬取、URL 发现和 AI 深度研究，返回针对 LLM 优化的 JSON 数据。

---

## Step 1: 检查 tvly 是否可用

```bash
tvly --status
```

预期输出：
```
tavily v0.1.0
> Authenticated via OAuth (tvly login)
```

若未安装，执行：

```bash
curl -fsSL https://cli.tavily.com/install.sh | bash
```

或使用 pip/uv：

```bash
uv tool install tavily-cli
# 或
pip install tavily-cli
```

然后认证（从项目 `.env` 读取 `TAVILY_API_KEY`，已认证则跳过）：

```bash
tvly --status 2>/dev/null | grep -q "Authenticated" || {
  set -a
  source "$(git rev-parse --show-toplevel 2>/dev/null || echo '.')/.env"
  set +a
  tvly login --api-key "$TAVILY_API_KEY"
}
```

---

## Step 2: 选择合适的命令

按以下升级路径选择，从简单开始，按需升级：

| 场景 | 命令 | 说明 |
|------|------|------|
| 无具体 URL，查找相关页面 | `tvly search` | 第一步 |
| 已有 URL，获取页面内容 | `tvly extract` | 第二步 |
| 大型站点，先发现 URL | `tvly map` | 第三步 |
| 批量提取整个站点章节 | `tvly crawl` | 第四步 |
| 需要多源综合分析报告 | `tvly research` | 第五步 |

---

## Step 3: 通用输出选项

所有命令均支持：

```bash
tvly search "react hooks" --json -o results.json
tvly extract "https://example.com/docs" -o docs.md
tvly crawl "https://docs.example.com" --output-dir ./docs/
```

- `--json`：结构化 JSON 输出，适合 agentic 工作流
- `-o <file>`：保存输出到文件
- `--output-dir <dir>`：（仅 crawl）每个页面保存为独立 .md 文件

---

## 注意事项

- **始终给 URL 加引号** — shell 会将 `?` 和 `&` 解析为特殊字符
- **agentic 工作流使用 `--json`** — 每个命令都支持
- **用 `-` 从 stdin 读取** — `echo "query" | tvly search -`
- **退出码**：0 = 成功，2 = 输入错误，3 = 认证错误，4 = API 错误
