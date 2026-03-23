==============================
skill_id: tavily_research
name: Tavily Research
display_name: AI 深度研究
enable: true
description: AI-powered deep research via tvly CLI. Gathers sources, analyzes, and produces cited reports. Takes 30-120s. For quick facts use tavily_search instead.
trigger: When the user needs deep research, comparisons, market analysis, literature review, or says "research", "调研", "深入分析", "对比分析".
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
==============================

# Tavily Research

Auth: see `tavily_cli` skill auth setup.

## Usage

```bash
tvly research "competitive landscape of AI code assistants"
tvly research "EV market analysis" --model pro --stream
tvly research "fintech trends 2025" --model pro -o report.md
tvly research "quantum computing" --json
```

## Key Parameters

| Param | Values |
|-------|--------|
| `--model` | `mini`(~30s, focused) / `pro`(~60-120s, comprehensive) / `auto`(default) |
| `--stream` | real-time progress output |
| `--citation-format` | `numbered` / `mla` / `apa` / `chicago` |
| `--output-schema` | JSON schema file for structured output |
| `-o` | save report to file |

Rule of thumb: "What is X?" → mini. "X vs Y vs Z" → pro.

## Async workflow

```bash
tvly research "topic" --no-wait --json    # returns request_id
tvly research status <id> --json          # check status
tvly research poll <id> --json -o out.json # wait & get result
```
