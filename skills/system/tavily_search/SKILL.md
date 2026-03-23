==============================
skill_id: tavily_search
name: Tavily Search
display_name: 网络搜索
enable: true
description: Web search via tvly CLI returning LLM-optimized results with snippets, scores, and metadata. Supports domain filtering, time ranges, and multiple search depths.
trigger: When the user wants to search the web, find articles, look up info, get recent news, or says "搜索", "查找", "最新的 X".
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
==============================

# Tavily Search

Auth: see `tavily_cli` skill auth setup.

## Usage

```bash
tvly search "query" --json
tvly search "AI news" --depth advanced --max-results 10 --time-range week --topic news --json
tvly search "docs" --include-domains docs.example.com --include-raw-content --json
```

## Key Parameters

| Param | Values |
|-------|--------|
| `--depth` | `ultra-fast` / `fast` / `basic`(default) / `advanced` |
| `--max-results` | 0-20, default 5 |
| `--topic` | `general` / `news` / `finance` |
| `--time-range` | `day` / `week` / `month` / `year` |
| `--include-domains` | comma-separated whitelist |
| `--exclude-domains` | comma-separated blacklist |
| `--include-raw-content` | full page content (saves separate extract) |
| `--include-answer` | `basic` or `advanced` AI answer |
| `--chunks-per-source` | chunks per result (advanced/fast only) |

Keep queries under 400 chars. Break complex queries into sub-queries.
