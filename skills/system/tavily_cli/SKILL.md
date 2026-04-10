---
skill_id: tavily_cli
name: Tavily CLI
display_name: 网络搜索与研究
enable: false
description: Web search, content extraction, site crawling, URL discovery, and AI deep research via the tvly CLI. Returns LLM-optimized JSON.
trigger: When the user wants to search the web, extract URL content, crawl docs, discover site URLs, or conduct deep research with citations.
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
---

# Tavily CLI

## Auth Setup

```bash
which tvly || curl -fsSL https://cli.tavily.com/install.sh | bash

tvly --status 2>/dev/null | grep -q "Authenticated" || {
  set -a
  source "$(git rev-parse --show-toplevel 2>/dev/null || echo '.')/.env"
  set +a
  tvly login --api-key "$TAVILY_API_KEY"
}
```

## Command Selection

| Need | Command |
|------|---------|
| Find pages on a topic | `tvly search "query" --json` |
| Get a page's content | `tvly extract "url" --json` |
| Find URLs on a site | `tvly map "url" --json` |
| Bulk extract site section | `tvly crawl "url" --json` |
| Multi-source research report | `tvly research "topic"` |

Escalation order: search → extract → map → crawl → research.

## Tips

- Always quote URLs (shell interprets `?` and `&`)
- Use `--json` for agentic workflows; `-o file` to save
- Read from stdin: `echo "query" | tvly search -`
- Exit codes: 0=ok, 2=bad input, 3=auth error, 4=API error
