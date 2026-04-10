---
skill_id: tavily_crawl
name: Tavily Crawl
display_name: 站点批量爬取
enable: false
description: Crawls websites and extracts content from multiple pages via tvly CLI. Supports depth/breadth control, path filtering, semantic instructions, and saving pages as local markdown files.
trigger: When the user wants to crawl a site, download docs, bulk-extract pages, or says "crawl", "download the docs", "批量抓取", "爬取".
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
---

# Tavily Crawl

Auth: see `tavily_cli` skill auth setup.

## Usage

```bash
# Basic
tvly crawl "https://docs.example.com" --json

# Save as local markdown files
tvly crawl "https://docs.example.com" --output-dir ./docs/

# Focused with limits
tvly crawl "https://example.com" --select-paths "/api/.*" --max-depth 2 --limit 50 --json

# Semantic focus (returns relevant chunks only)
tvly crawl "https://docs.example.com" --instructions "Find auth docs" --chunks-per-source 3 --json
```

## Key Parameters

| Param | Values |
|-------|--------|
| `--max-depth` | 1-5, default 1 |
| `--limit` | total page cap, default 50 |
| `--instructions` | semantic focus (2 credits/10 pages) |
| `--chunks-per-source` | 1-5, requires `--instructions` |
| `--select-paths` / `--exclude-paths` | regex patterns |
| `--extract-depth` | `basic` or `advanced` |
| `--output-dir` | save each page as .md file |

**Agentic use**: always use `--instructions` + `--chunks-per-source` to prevent context explosion.
**Data collection**: use `--output-dir` for full pages.

Start conservative (`--max-depth 1`, `--limit 20`). Always set `--limit`. Use map first to understand structure.
