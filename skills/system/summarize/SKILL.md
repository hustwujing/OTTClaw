==============================
skill_id: summarize
name: Summarize
display_name: 内容摘要
enable: true
description: 使用 summarize CLI 快速摘要 URL、本地文件和 YouTube 视频内容，也可提取字幕/转录文本。支持多种 AI 模型和输出长度配置。
trigger: 当用户说"总结这个链接"、"这个视频讲了什么"、"帮我摘要这篇文章"、"提取字幕"、"转录这个 YouTube 视频"、"summarize this URL/article"、"what's this link/video about" 等时触发。
requires_bins: summarize
install_hint: brew install steipete/tap/summarize
==============================

# Summarize

使用 `summarize` CLI 工具对 URL、本地文件、YouTube 链接进行内容摘要或字幕提取。

---

## Step 1: 检查 summarize 是否已安装

```bash
which summarize
```

若未安装，提示用户执行：

```bash
brew install steipete/tap/summarize
```

---

## Step 2: 从 .env 读取 LLM 配置

执行任何 summarize 命令前，先从项目根目录的 `.env` 加载 LLM 配置并映射到 summarize 所需的环境变量：

```bash
# 加载 .env（不污染当前 shell）
set -a
source "$(git rev-parse --show-toplevel 2>/dev/null || echo '.')/.env"
set +a

# 映射到 summarize 所需的环境变量
# 内部代理（LLM_BASE_URL 非空）统一走 OpenAI 兼容协议
if [ -n "$LLM_BASE_URL" ]; then
  export OPENAI_API_KEY="$LLM_API_KEY"
  export OPENAI_BASE_URL="$LLM_BASE_URL/v1"
  SUMMARIZE_MODEL="openai/$LLM_MODEL"
elif [ "$LLM_PROVIDER" = "anthropic" ]; then
  export ANTHROPIC_API_KEY="$LLM_API_KEY"
  SUMMARIZE_MODEL="anthropic/$LLM_MODEL"
else
  export OPENAI_API_KEY="$LLM_API_KEY"
  SUMMARIZE_MODEL="openai/$LLM_MODEL"
fi
```

---

## Step 3: 根据输入类型执行命令

### 摘要 URL / 文章

```bash
summarize "https://example.com" --model "$SUMMARIZE_MODEL"
```

### 摘要本地文件（PDF、TXT 等）

```bash
summarize "/path/to/file.pdf" --model "$SUMMARIZE_MODEL"
```

### YouTube 视频摘要

```bash
summarize "https://youtu.be/dQw4w9WgXcQ" --youtube auto --model "$SUMMARIZE_MODEL"
```

### 仅提取 YouTube 字幕/转录（不摘要）

```bash
summarize "https://youtu.be/dQw4w9WgXcQ" --youtube auto --extract-only
```

> 若字幕内容很长，先返回简要摘要，再询问用户需要展开哪个时间段或章节。

---

## 常用参数

| 参数 | 说明 |
|------|------|
| `--model <provider/model>` | 由 .env 自动推导，通常无需手动指定 |
| `--length short\|medium\|long\|xl\|xxl\|<chars>` | 控制输出长度 |
| `--max-output-tokens <count>` | 限制输出 token 数 |
| `--extract-only` | 仅提取原文，不摘要（仅 URL） |
| `--json` | 输出机器可读 JSON |
| `--firecrawl auto\|off\|always` | 对被屏蔽站点启用 Firecrawl 回退 |
| `--youtube auto` | 启用 YouTube 字幕提取（可配合 Apify） |

---

## 模型配置来源

模型由项目 `.env` 中的以下变量决定：

| .env 变量 | 说明 |
|-----------|------|
| `LLM_MODEL` | 模型名，如 `claude-4.5-opus` |
| `LLM_PROVIDER` | `anthropic` / `openai` |
| `LLM_BASE_URL` | 自定义代理地址（非空时走 OpenAI 兼容协议） |
| `LLM_API_KEY` | API 密钥 |

**映射规则**：`LLM_BASE_URL` 非空时，无论 `LLM_PROVIDER` 值，均视为 OpenAI 兼容接口，设置 `OPENAI_API_KEY` + `OPENAI_BASE_URL`，模型格式为 `openai/$LLM_MODEL`。

---

## 可选服务

以下变量已在 Step 2 的 `source .env` 中自动加载，在 `.env` 中填写即可生效，无需手动 export：

- `FIRECRAWL_API_KEY`：用于访问被反爬拦截的网站（配合 `--firecrawl auto`）
- `APIFY_API_TOKEN`：YouTube 字幕提取的备用方案（配合 `--youtube auto`）

---

## 输出格式

直接将摘要内容展示给用户。若用户请求的是转录/字幕，先给出摘要，再询问是否需要完整文本或特定片段。
