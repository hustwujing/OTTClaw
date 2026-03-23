# Search API Reference

> API keys: always read from `.env`, never hardcode.

## Query Optimization

Keep queries < 400 chars. Break complex queries into sub-queries and run in parallel with `asyncio.gather`.

## Search Depth

| Depth | Latency | Content | Use case |
|-------|---------|---------|----------|
| `ultra-fast` | Lowest | NLP summary | Real-time chat |
| `fast` | Low | Chunks | Latency-sensitive |
| `basic` | Medium | NLP summary | General-purpose |
| `advanced` | Higher | Chunks (reranked) | Precision queries (recommended default) |

## Key Parameters

| Param | Type | Default | Notes |
|-------|------|---------|-------|
| `query` | str | Required | < 400 chars |
| `search_depth` | enum | `"basic"` | `ultra-fast/fast/basic/advanced` |
| `topic` | enum | `"general"` | `general/news/finance` |
| `max_results` | int | 5 | 0-20 |
| `chunks_per_source` | int | 3 | advanced/fast only |
| `time_range` | enum | null | `day/week/month/year` |
| `start_date`/`end_date` | str | null | `YYYY-MM-DD` |
| `include_domains` | arr | [] | max 300, supports `*.com` wildcards |
| `exclude_domains` | arr | [] | max 150 |
| `country` | enum | null | Boost results from country |
| `include_answer` | bool/enum | false | `true/"basic"/"advanced"` — skip if using own LLM |
| `include_raw_content` | bool/enum | false | `true/"markdown"/"text"` for full page |
| `include_images` | bool | false | |
| `auto_parameters` | bool | false | May set advanced depth (2 credits) |

## Response Fields

| Field | Description |
|-------|-------------|
| `results[].title/url/content/score` | Core result fields |
| `results[].raw_content` | Full page (if `include_raw_content`) |
| `answer` | AI answer (if `include_answer`) |
| `images[].url/description` | Image results |

## Post-Filtering

**Score-based**: `[r for r in results if r["score"] > 0.7]`

**Regex-based**: Validate URL patterns, required/excluded terms:
```python
import re
def regex_filter(result, criteria):
    full_text = f"{result.get('content','')} {result.get('title','')}".lower()
    if "url_pattern" in criteria:
        if not re.search(criteria["url_pattern"], result["url"].lower()): return False
    if "required_terms" in criteria:
        if not all(t.lower() in full_text for t in criteria["required_terms"]): return False
    return True
```

**LLM verification**: Use LLM to semantically validate results against criteria (synonyms, context).

[Full API reference](https://docs.tavily.com/documentation/api-reference/endpoint/search)
