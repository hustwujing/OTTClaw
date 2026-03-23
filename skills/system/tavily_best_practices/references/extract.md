# Extract API Reference

> API keys: always read from `.env`, never hardcode.

## Two Approaches

1. **One-step**: `client.search(query, include_raw_content=True)` — quick prototyping
2. **Two-step (recommended)**: Search → filter by score → `client.extract(urls, query, chunks_per_source)` — more control

## Key Parameters

| Param | Type | Default | Notes |
|-------|------|---------|-------|
| `urls` | str/arr | Required | Max 20 URLs |
| `extract_depth` | enum | `"basic"` | `advanced` for JS/SPA pages |
| `query` | str | null | Reranks chunks by relevance |
| `chunks_per_source` | int | 3 | 1-5, max 500 chars each, requires `query` |
| `format` | enum | `"markdown"` | `markdown/text` |
| `timeout` | float | varies | 1.0-60.0s |

## Query + Chunks

Use `query` + `chunks_per_source` to prevent context explosion:
```python
extracted = client.extract(
    urls=["url1","url2","url3"],
    query="AI diagnostic tools accuracy",
    chunks_per_source=2  # returns top 2 relevant chunks per URL
)
```
Chunks in `raw_content`: `<chunk 1> [...] <chunk 2>`

## Extract Depth

- `basic`: Simple text, faster
- `advanced`: JS-rendered, tables, structured data

Fallback strategy: try basic first, retry with advanced if URL in `failed_results`.

## Response Fields

| Field | Description |
|-------|-------------|
| `results[].url` | Extracted URL |
| `results[].raw_content` | Full content or ranked chunks |
| `failed_results[].url/error` | Failed extractions |

## Pipeline Pattern

```python
# 1. Search with sub-queries → 2. Filter by score > 0.5 → 3. Deduplicate
# 4. Extract with query + chunks → 5. Validate content
```

[Full API reference](https://docs.tavily.com/documentation/api-reference/endpoint/extract)
