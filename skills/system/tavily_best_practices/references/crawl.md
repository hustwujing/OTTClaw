# Crawl & Map API Reference

> API keys: always read from `.env`, never hardcode.

## Crawl vs Map

| | Crawl | Map |
|--|-------|-----|
| Returns | Full content | URLs only |
| Speed | Slower | Faster |
| Use | RAG, deep analysis, docs | Site structure, URL discovery |

## Key Parameters

| Param | Type | Default | Notes |
|-------|------|---------|-------|
| `url` | str | Required | Root URL |
| `max_depth` | int | 1 | 1-5, start with 1-2 |
| `max_breadth` | int | 20 | Links per page |
| `limit` | int | 50 | Total page cap, **always set** |
| `instructions` | str | null | Semantic focus (2 credits/10 pages) |
| `chunks_per_source` | int | 3 | 1-5, requires `instructions` |
| `extract_depth` | enum | `"basic"` | `basic` (1cr/5URL) / `advanced` (2cr/5URL) |
| `select_paths`/`exclude_paths` | arr | null | Regex patterns |
| `select_domains`/`exclude_domains` | arr | null | Regex patterns |
| `allow_external` | bool | true(crawl)/false(map) | |

## Instructions + Chunks

```python
response = client.crawl(
    url="https://docs.example.com", max_depth=2,
    instructions="Find auth and security docs",
    chunks_per_source=3  # prevents context explosion
)
```

## Path Filtering

```python
response = client.crawl(
    url="https://example.com",
    select_paths=["/docs/.*", "/api/.*"],
    exclude_paths=["/blog/.*", "/private/.*"]
)
```

## Map + Extract Pattern

```python
# 1. Map to discover structure
urls = client.map(url="https://docs.example.com", instructions="Find API docs")
# 2. Filter discovered URLs
api_docs = [u for u in urls["results"] if "/api/" in u]
# 3. Extract from filtered URLs
client.extract(urls=api_docs[:20], query="API endpoints", chunks_per_source=3)
```

## Performance

| Depth | Pages | Time |
|-------|-------|------|
| 1 | 10-50 | Seconds |
| 2 | 50-500 | Minutes |
| 3 | 500-5000 | Many minutes |

Start conservative (`max_depth=1`, `limit=20`), scale up as needed.

## Common Pitfalls

| Problem | Solution |
|---------|----------|
| Excessive depth | Start with 1-2 |
| Context explosion | Use `instructions` + `chunks_per_source` |
| Runaway crawls | Always set `limit` |
| Incomplete data | Monitor `failed_results` |

## Response

- **Crawl**: `base_url`, `results[].{url, raw_content, images, favicon}`
- **Map**: `base_url`, `results[]` (URL strings)

[Full API reference](https://docs.tavily.com/documentation/api-reference/endpoint/crawl)
