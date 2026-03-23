# Framework Integrations

> API keys: always read from `.env`, never hardcode.

## LangChain

```bash
pip install -U langchain-tavily
```

```python
from langchain_tavily import TavilySearch, TavilyExtract, TavilyMap, TavilyCrawl, TavilyResearch

tavily_search = TavilySearch(max_results=5, topic="general")
result = tavily_search.invoke({"query": "..."})

# Use with agent
from langchain.agents import create_agent
agent = create_agent(model=ChatOpenAI(model="gpt-5"), tools=[tavily_search])
```

Available tools: `TavilySearch`, `TavilyExtract`, `TavilyMap`, `TavilyCrawl`, `TavilyResearch`, `TavilyGetResearch`

## Pydantic AI

```bash
pip install "pydantic-ai-slim[tavily]"
```

```python
from pydantic_ai.common_tools.tavily import tavily_search_tool
agent = Agent("openai:o3-mini", tools=[tavily_search_tool(api_key)])
```

## LlamaIndex

```python
from llama_index.tools.tavily_research import TavilyToolSpec
tools = TavilyToolSpec(api_key=os.getenv("TAVILY_API_KEY")).to_tool_list()
agent = OpenAIAgent.from_tools(tools)
```

## Agno

```bash
pip install agno tavily-python
```

```python
from agno.tools.tavily import TavilyTools
agent = Agent(tools=[TavilyTools(search=True, search_depth="advanced", format="markdown")])
```

## OpenAI Function Calling

```python
tools = [{"type": "function", "function": {
    "name": "web_search",
    "parameters": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}
}}]
# Handle tool_calls → tavily_client.search(args["query"]) → feed results back
```

## Anthropic Tool Calling

```python
tools = [{"name": "tavily_search", "input_schema": {
    "type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]
}}]
# Claude tool_use → tavily_client.search(**args) → send tool_result back
```

## Google ADK (via MCP)

```python
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset
MCPToolset(connection_params=StreamableHTTPServerParams(
    url="https://mcp.tavily.com/mcp/",
    headers={"Authorization": f"Bearer {tavily_api_key}"}
))
```

## Vercel AI SDK

```bash
npm install ai @ai-sdk/openai @tavily/ai-sdk
```

```typescript
import { tavilySearch, tavilyCrawl, tavilyExtract, tavilyMap } from "@tavily/ai-sdk";
const result = await generateText({
  model: openai("gpt-4"), prompt: "...",
  tools: { tavilySearch: tavilySearch({ maxResults: 5 }) }
});
```

## CrewAI

```python
from crewai_tools import TavilySearchTool, TavilyExtractTool
researcher = Agent(role="Research Analyst", tools=[TavilySearchTool()])
```

## No-Code Platforms

Zapier, Make, n8n, Dify, FlowiseAI, Langflow — all support Tavily Search/Extract.

[Full integrations docs](https://docs.tavily.com/documentation/integrations)
