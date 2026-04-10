---
skill_id: tavily_best_practices
name: Tavily Best Practices
display_name: Tavily 最佳实践
enable: false
description: Production-grade Tavily SDK reference for Python & JavaScript. Client init, method selection, quick examples, and links to detailed reference docs.
trigger: When the user integrates Tavily SDK in code, builds RAG/agentic workflows, or asks "how to use Tavily in code", "Tavily SDK".
---

# Tavily SDK Best Practices

Install: `pip install tavily-python` (Python) / `npm install @tavily/core` (JS)

## Client Init

```python
from tavily import TavilyClient
client = TavilyClient()  # uses TAVILY_API_KEY env var

from tavily import AsyncTavilyClient
async_client = AsyncTavilyClient()  # for parallel queries
```

## Method Selection

| Need | Method |
|------|--------|
| Web search | `client.search(query, search_depth="advanced", max_results=10)` |
| URL content | `client.extract(urls=[...], extract_depth="advanced")` |
| Site crawl | `client.crawl(url, instructions="...", chunks_per_source=3)` |
| URL discovery | `client.map(url)` |
| Deep research | `client.research(input="...", model="pro")` |

## Key Constraints

- `search()`: query < 400 chars
- `extract()`: max 20 URLs per call
- `crawl()`: always set `limit`
- `research()`: 30-120s, use `stream=True` for progress

## Detailed References

Load on-demand when deeper API details are needed:

> **Note**: Reference files use placeholder API keys (e.g., `"tvly-YOUR_API_KEY"`). In OTTClaw, always read keys from `.env`.

| Topic | Reference |
|-------|-----------|
| SDK init, async, Hybrid RAG | `skill(action=read_file, skill_id=tavily_best_practices, sub_path="references/sdk.md")` |
| search() params, filtering, post-processing | `skill(action=read_file, skill_id=tavily_best_practices, sub_path="references/search.md")` |
| extract() one-step vs two-step, query/chunks | `skill(action=read_file, skill_id=tavily_best_practices, sub_path="references/extract.md")` |
| crawl() & map() instructions, Map+Extract | `skill(action=read_file, skill_id=tavily_best_practices, sub_path="references/crawl.md")` |
| research() prompting, models, streaming | `skill(action=read_file, skill_id=tavily_best_practices, sub_path="references/research.md")` |
| LangChain, CrewAI, Vercel AI SDK integrations | `skill(action=read_file, skill_id=tavily_best_practices, sub_path="references/integrations.md")` |
