# Research API Reference

> API keys: always read from `.env`, never hardcode.

## Overview

End-to-end AI research: automatic source gathering, analysis, and cited report generation.

## Prompting Tips

- Be specific: include target market, competitors, geography, constraints
- Share prior context to avoid repeating known info
- Keep prompts clean: clear task + essential context + desired output format

## Model Selection

| Model | Best for | Time |
|-------|----------|------|
| `mini` | Narrow, well-scoped questions | ~30s |
| `pro` | Complex multi-domain analysis | 60-120s |
| `auto` | When unsure (default) | varies |

## Key Parameters

| Param | Type | Default | Notes |
|-------|------|---------|-------|
| `input` | str | Required | Research topic |
| `model` | enum | `"auto"` | `mini/pro/auto` |
| `stream` | bool | false | Real-time progress |
| `output_schema` | obj | null | JSON Schema for structured output |
| `citation_format` | enum | `"numbered"` | `numbered/mla/apa/chicago` |

## Basic Usage (Poll)

```python
result = client.research(input="...", model="pro")
response = client.get_research(result["request_id"])
while response["status"] not in ["completed", "failed"]:
    time.sleep(10)
    response = client.get_research(result["request_id"])
report = response["content"]
sources = response["sources"]
```

## Streaming

```python
stream = client.research(input="...", model="pro", stream=True)
for chunk in stream:
    print(chunk.decode('utf-8'))
```

Event types: `Tool Call` (Planning/WebSearch/ResearchSubtopic/Generating) → `Content` → `Sources` → `Done`

## Structured Output

```python
result = client.research(
    input="EV market analysis",
    output_schema={
        "properties": {
            "summary": {"type": "string", "description": "Executive summary"},
            "key_points": {"type": "array", "items": {"type": "string"}}
        },
        "required": ["summary", "key_points"]
    }
)
```

Schema tips: clear field descriptions, match needed structure, use `required`, supported types: `object/string/integer/number/array`.

## Response Fields

| Field | Description |
|-------|-------------|
| `request_id` | Tracking ID |
| `status` | `pending/processing/completed/failed` |
| `content` | Research report |
| `sources[].url/title/citation` | Citations |

[Cookbook](https://github.com/tavily-ai/tavily-cookbook/tree/main/research) | [Live demo](https://chat-research.tavily.com/)
