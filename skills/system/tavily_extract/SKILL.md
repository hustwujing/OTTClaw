==============================
skill_id: tavily_extract
name: Tavily Extract
display_name: 网页内容提取
enable: true
description: Extracts clean markdown/text from URLs via tvly CLI. Handles JS-rendered pages. Supports query-focused chunking. Max 20 URLs per call.
trigger: When the user has URLs and wants their content, says "提取", "抓取页面", "extract", "grab the content from", "read this webpage".
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
==============================

# Tavily Extract

Auth: see `tavily_cli` skill auth setup.

## Usage

```bash
tvly extract "https://example.com/page" --json
tvly extract "url1" "url2" --query "auth API" --chunks-per-source 3 --json
tvly extract "https://spa.example.com" --extract-depth advanced --json
tvly extract "https://example.com" -o output.md
```

## Key Parameters

| Param | Values |
|-------|--------|
| `--extract-depth` | `basic`(default) or `advanced`(JS/SPA) |
| `--query` | rerank chunks by relevance |
| `--chunks-per-source` | 1-5, requires `--query` |
| `--format` | `markdown`(default) or `text` |
| `--timeout` | 1-60 seconds |

Max 20 URLs per request. Try `basic` first, fall back to `advanced` if content missing.
