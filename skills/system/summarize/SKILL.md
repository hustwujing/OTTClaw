==============================
skill_id: summarize
name: Summarize
display_name: 内容摘要
enable: true
description: Summarizes URLs, local files, and YouTube videos via the summarize CLI. Also extracts transcripts. Uses LLM config from project .env.
trigger: When the user says "总结这个链接", "这个视频讲了什么", "帮我摘要", "提取字幕", "summarize this URL", "what's this link about".
requires_bins: summarize
install_hint: brew install steipete/tap/summarize
==============================

# Summarize

## Step 1: Check installation

```bash
which summarize || echo "Not installed — run: brew install steipete/tap/summarize"
```

## Step 2: Load LLM config from .env

```bash
set -a
source "$(git rev-parse --show-toplevel 2>/dev/null || echo '.')/.env"
set +a

if [ -n "$LLM_BASE_URL" ]; then
  export OPENAI_API_KEY="$LLM_API_KEY"
  export OPENAI_BASE_URL="$LLM_BASE_URL/v1"
  SUMMARIZE_MODEL="openai/$LLM_MODEL"
elif [ "$LLM_PROVIDER" = "anthropic" ]; then
  export ANTHROPIC_API_KEY="$LLM_API_KEY"
  SUMMARIZE_MODEL="anthropic/$LLM_MODEL"
else
  export OPENAI_API_KEY="$LLM_API_KEY"
  SUMMARIZE_MODEL="openai/$LLM_MODEL"
fi
```

Model mapping: `LLM_BASE_URL` non-empty → OpenAI-compatible proxy; otherwise use `LLM_PROVIDER` directly.

## Step 3: Run

```bash
# URL / article
summarize "https://example.com" --model "$SUMMARIZE_MODEL"

# Local file
summarize "/path/to/file.pdf" --model "$SUMMARIZE_MODEL"

# YouTube
summarize "https://youtu.be/xxx" --youtube auto --model "$SUMMARIZE_MODEL"

# YouTube transcript only (no summary)
summarize "https://youtu.be/xxx" --youtube auto --extract-only
```

If transcript is huge, return a summary first, then ask which section to expand.

## Key flags

| Flag | Purpose |
|------|---------|
| `--length short\|medium\|long\|xl\|xxl` | Output length |
| `--max-output-tokens N` | Token limit |
| `--extract-only` | Raw text only, no summary (URLs only) |
| `--json` | Machine-readable output |
| `--firecrawl auto` | Anti-crawl fallback (needs `FIRECRAWL_API_KEY` in .env) |
| `--youtube auto` | YouTube transcript (needs `APIFY_API_TOKEN` in .env for fallback) |
