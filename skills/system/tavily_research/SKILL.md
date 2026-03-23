==============================
skill_id: tavily_research
name: Tavily Research
display_name: AI 深度研究
enable: true
description: 通过 Tavily CLI 进行带引用的 AI 深度研究，自动收集多个来源、分析并生成结构化报告。适用于需要综合分析、市场调研、竞品对比、文献综述等场景。耗时 30-120 秒。
trigger: 当用户需要深度研究、详细报告、竞品对比、市场分析、文献综述，或说"research"、"investigate"、"analyze in depth"、"compare X vs Y"、"market landscape"、"调研"、"深入分析"、"对比分析"、"需要带引用的综合报告"时触发。快速事实查询请用 tavily_search 代替。
requires_bins: tvly
install_hint: curl -fsSL https://cli.tavily.com/install.sh | bash
==============================

# Tavily Research

AI 深度研究：自动收集来源、分析并生成带引用的结构化报告。耗时 30-120 秒。

---

## 前置检查

**Step 1：检查安装**

```bash
which tvly || curl -fsSL https://cli.tavily.com/install.sh | bash
```

**Step 2：检查认证，未认证则从 `.env` 加载 `TAVILY_API_KEY`**

```bash
tvly --status 2>/dev/null | grep -q "Authenticated" || {
  set -a
  source "$(git rev-parse --show-toplevel 2>/dev/null || echo '.')/.env"
  set +a
  tvly login --api-key "$TAVILY_API_KEY"
}
```

---

## 快速开始

```bash
# 基础研究（等待完成）
tvly research "competitive landscape of AI code assistants"

# pro 模型做综合分析
tvly research "electric vehicle market analysis" --model pro

# 实时流式输出
tvly research "AI agent frameworks comparison" --stream

# 保存报告到文件
tvly research "fintech trends 2025" --model pro -o fintech-report.md

# JSON 输出（适合 agentic 工作流）
tvly research "quantum computing breakthroughs" --json
```

---

## 常用参数

| 参数 | 说明 |
|------|------|
| `--model` | `mini` / `pro` / `auto`（默认） |
| `--stream` | 实时流式输出，可以看到进度 |
| `--no-wait` | 立即返回 request_id（异步模式） |
| `--output-schema` | 结构化输出的 JSON Schema 文件路径 |
| `--citation-format` | `numbered` / `mla` / `apa` / `chicago` |
| `--poll-interval` | 轮询间隔秒数（默认 10） |
| `--timeout` | 最大等待秒数（默认 600） |
| `-o, --output` | 保存到文件 |
| `--json` | 结构化 JSON 输出 |

---

## 模型选择

| 模型 | 适用场景 | 速度 |
|------|----------|------|
| `mini` | 单一话题、目标明确的研究 | ~30 秒 |
| `pro` | 多角度综合分析、复杂对比 | ~60-120 秒 |
| `auto` | 让 API 根据复杂度自动选择 | 不定 |

**经验法则**："X 是什么？" → mini。"X vs Y vs Z 对比" 或 "最佳方案分析" → pro。

---

## 异步工作流

对于长时间研究任务，可以先启动再轮询：

```bash
# 启动，不等待
tvly research "topic" --no-wait --json    # 返回 request_id

# 检查状态
tvly research status <request_id> --json

# 等待并获取结果
tvly research poll <request_id> --json -o result.json
```

---

## 使用技巧

- **研究需要 30-120 秒** — 使用 `--stream` 实时查看进度
- **复杂对比或多维话题使用 `--model pro`**
- **使用 `--output-schema`** 获取符合自定义结构的 JSON 输出
- **快速事实查询使用 `tvly search`** — research 适合深度综合分析
- 从 stdin 读取：`echo "query" | tvly research - --json`
