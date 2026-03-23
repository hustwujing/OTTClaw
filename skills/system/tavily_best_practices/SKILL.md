==============================
skill_id: tavily_best_practices
name: Tavily Best Practices
display_name: Tavily 最佳实践
enable: true
description: Tavily Python/JavaScript SDK 的生产级集成最佳实践参考。包含客户端初始化、各方法适用场景选择、SDK 快速参考和详细代码示例。适合在代码中集成 Tavily API 的开发者使用。
trigger: 当用户在代码中集成 Tavily SDK、使用 Python/JavaScript 调用 Tavily API、构建 RAG 系统或 agentic 工作流时触发。适用于"如何在代码里用 Tavily"、"Tavily SDK 怎么用"、"集成 Tavily 到我的项目"等场景。
==============================

# Tavily SDK 最佳实践

Tavily 是专为 LLM 设计的搜索 API，让 AI 应用获得实时网络数据。

---

## 安装

**Python：**
```bash
pip install tavily-python
```

**JavaScript：**
```bash
npm install @tavily/core
```

---

## 客户端初始化

```python
from tavily import TavilyClient

# 推荐：使用 TAVILY_API_KEY 环境变量
client = TavilyClient()

# 带项目追踪（用于用量统计）
client = TavilyClient(project_id="your-project-id")

# 异步客户端（用于并行查询）
from tavily import AsyncTavilyClient
async_client = AsyncTavilyClient()
```

---

## 选择合适的方法

**自定义 agent / 工作流：**

| 需求 | 方法 |
|------|------|
| 网络搜索结果 | `search()` |
| 指定 URL 的内容 | `extract()` |
| 整个站点的内容 | `crawl()` |
| 站点 URL 发现 | `map()` |

**开箱即用的研究：**

| 需求 | 方法 |
|------|------|
| 端到端研究 + AI 综合报告 | `research()` |

---

## SDK 快速参考

### search() - 网络搜索

```python
response = client.search(
    query="quantum computing breakthroughs",  # 保持在 400 字符以内
    max_results=10,
    search_depth="advanced"
)
print(response)
```

主要参数：`query`、`max_results`、`search_depth`（ultra-fast/fast/basic/advanced）、`include_domains`、`exclude_domains`、`time_range`

---

### extract() - URL 内容提取

```python
response = client.extract(
    urls=["https://docs.example.com"],
    extract_depth="advanced"
)
print(response)
```

主要参数：`urls`（最多 20 个）、`extract_depth`、`query`、`chunks_per_source`（1-5）

---

### crawl() - 站点批量提取

```python
response = client.crawl(
    url="https://docs.example.com",
    instructions="Find API documentation pages",  # 语义聚焦
    extract_depth="advanced"
)
print(response)
```

主要参数：`url`、`max_depth`、`max_breadth`、`limit`、`instructions`、`chunks_per_source`、`select_paths`、`exclude_paths`

---

### map() - URL 发现

```python
response = client.map(
    url="https://docs.example.com"
)
print(response)
```

---

### research() - AI 深度研究

```python
import time

result = client.research(
    input="Analyze competitive landscape for X in SMB market",
    model="pro"  # "mini" 适合聚焦查询，"auto" 自动选择
)
request_id = result["request_id"]

# 轮询直到完成
response = client.get_research(request_id)
while response["status"] not in ["completed", "failed"]:
    time.sleep(10)
    response = client.get_research(request_id)

print(response["content"])  # 研究报告
```

主要参数：`input`、`model`（"mini"/"pro"/"auto"）、`stream`、`output_schema`、`citation_format`

---

## 异步并行查询示例

```python
import asyncio
from tavily import AsyncTavilyClient

async def parallel_search():
    async_client = AsyncTavilyClient()
    queries = ["query 1", "query 2", "query 3"]
    results = await asyncio.gather(*[
        async_client.search(q) for q in queries
    ])
    return results
```

---

## 注意事项

- **search() 查询保持在 400 字符以内**
- **extract() 单次最多 20 个 URL**
- **crawl() 始终设置 `limit` 参数** 防止失控
- **research() 耗时 30-120 秒**，建议异步或流式处理
- **使用 `AsyncTavilyClient`** 进行并行查询以提升性能
