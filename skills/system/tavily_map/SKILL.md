---
skill_id: tavily_map
name: Tavily Map
display_name: 站点 URL 发现
enable: false
description: Discovers URLs on a website without extracting content. Faster than crawling. Use to find specific pages before extract/crawl.
trigger: When the user wants to list URLs on a site, find a specific page, see site structure, or says "map the site", "list all pages", "站点地图".
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
---

# Tavily Map

Auth: see `tavily_cli` skill auth setup.

## Usage

```bash
tvly map "https://docs.example.com" --json
tvly map "https://example.com" --instructions "Find API docs" --json
tvly map "https://example.com" --select-paths "/blog/.*" --limit 500 --json
```

## Key Parameters

| Param | Values |
|-------|--------|
| `--max-depth` | 1-5, default 1 |
| `--limit` | max URLs, default 50 |
| `--instructions` | natural language URL filtering |
| `--select-paths` / `--exclude-paths` | regex patterns |

## Map + Extract pattern

```bash
tvly map "https://docs.example.com" --instructions "authentication" --json
# then extract the specific URL found
tvly extract "https://docs.example.com/api/auth" --json
```

Map+extract beats crawl when you only need a few pages from a large site.
