# SDK Reference

> API keys: always read from `.env`, never hardcode.

## Python

```bash
pip install tavily-python
```

```python
from tavily import TavilyClient
client = TavilyClient()          # uses TAVILY_API_KEY env var

from tavily import AsyncTavilyClient
async_client = AsyncTavilyClient()
responses = await asyncio.gather(
    async_client.search("q1"), async_client.search("q2")
)
```

### Methods

```python
# search()
response = client.search(
    query="...", search_depth="advanced", topic="general",
    max_results=10, include_answer=False, include_raw_content=False,
    time_range="week", include_domains=["arxiv.org"], exclude_domains=["reddit.com"]
)

# extract()
response = client.extract(
    urls=["url1","url2"], extract_depth="basic", format="markdown",
    query="focus query", chunks_per_source=3
)

# crawl()
response = client.crawl(
    url="https://docs.example.com", max_depth=2, limit=50,
    instructions="Find API docs", chunks_per_source=3,
    select_paths=["/docs/.*"], extract_depth="basic"
)

# map()
response = client.map(url="https://docs.example.com", max_depth=2, limit=50)

# research()
result = client.research(input="...", model="pro", citation_format="numbered")
response = client.get_research(result["request_id"])  # poll
```

## JavaScript

```bash
npm install @tavily/core
```

```javascript
const { tavily } = require("@tavily/core");
const client = tavily({ apiKey: process.env.TAVILY_API_KEY });

// search
const r = await client.search("query", { searchDepth: "advanced", maxResults: 10 });
// extract
const e = await client.extract(["url1"], { extractDepth: "basic", query: "..." });
// crawl
const c = await client.crawl("url", { maxDepth: 2, limit: 50, instructions: "..." });
// map
const m = await client.map("url", { maxDepth: 2, limit: 50 });
```

## Hybrid RAG

```python
from tavily import TavilyHybridClient
hybrid_client = TavilyHybridClient(
    db_provider="mongodb", collection=db.get_collection("docs"),
    embeddings_field="embeddings", content_field="content"
)
results = hybrid_client.search("query", max_local=5, max_foreign=5, save_foreign=True)
```

## Response Structures

| Method | Key fields |
|--------|-----------|
| search | `query`, `results[].{title,url,content,score}`, `answer`, `images` |
| extract | `results[].{url,raw_content}`, `failed_results[].{url,error}` |
| crawl | `base_url`, `results[].{url,raw_content}` |
| map | `base_url`, `results[]` (URL strings) |

Docs: [Python SDK](https://docs.tavily.com/sdk/python/reference) | [JS SDK](https://docs.tavily.com/sdk/javascript/reference)
