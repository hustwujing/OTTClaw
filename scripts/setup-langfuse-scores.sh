#!/usr/bin/env bash
# Author:    Vijay
# Email:     hustwujing@163.com
# Date:      2026
# Copyright: Copyright (c) 2026 Vijay
#
# scripts/setup-langfuse-scores.sh — 在 Langfuse 创建 Task Unit 评估所需的 Score Config
#
# 用法：
#   bash scripts/setup-langfuse-scores.sh
#
# 依赖：curl、jq（macOS: brew install jq）
# 配置来源：自动读取项目根目录的 .env 文件
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# ── 读取 .env ─────────────────────────────────────────────────────────────────
ENV_FILE="$ROOT_DIR/.env"
if [ ! -f "$ENV_FILE" ]; then
  echo "错误：未找到 $ENV_FILE，请先复制 .env.example 并填写配置"
  exit 1
fi

# 仅加载 LANGFUSE_* 相关变量，忽略注释和空行
while IFS= read -r line; do
  [[ "$line" =~ ^#.*$ || -z "$line" ]] && continue
  [[ "$line" =~ ^LANGFUSE_ ]] || continue
  export "${line?}"
done < "$ENV_FILE"

BASE_URL="${LANGFUSE_BASE_URL:-}"
PK="${LANGFUSE_PUBLIC_KEY:-}"
SK="${LANGFUSE_SECRET_KEY:-}"

if [ -z "$BASE_URL" ] || [ -z "$PK" ] || [ -z "$SK" ]; then
  echo "错误：请在 .env 中填写 LANGFUSE_BASE_URL / LANGFUSE_PUBLIC_KEY / LANGFUSE_SECRET_KEY"
  exit 1
fi

BASE_URL="${BASE_URL%/}"  # 去掉末尾斜杠
AUTH="$(printf '%s:%s' "$PK" "$SK" | base64)"
ENDPOINT="$BASE_URL/api/public/score-configs"

# ── 依赖检查 ──────────────────────────────────────────────────────────────────
for cmd in curl jq; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "错误：未找到 $cmd，请先安装（macOS: brew install $cmd）"
    exit 1
  fi
done

# ── 已存在则跳过 ──────────────────────────────────────────────────────────────
existing_names() {
  curl -sf "$ENDPOINT?limit=100" \
    -H "Authorization: Basic $AUTH" \
    | jq -r '.data[].name' 2>/dev/null || true
}

EXISTING="$(existing_names)"

create_config() {
  local name="$1"
  local payload="$2"

  if echo "$EXISTING" | grep -qx "$name"; then
    echo "  [跳过] $name 已存在"
    return
  fi

  local resp
  resp=$(curl -sf -X POST "$ENDPOINT" \
    -H "Authorization: Basic $AUTH" \
    -H "Content-Type: application/json" \
    -d "$payload" 2>&1) || {
    echo "  [失败] $name — $resp"
    return 1
  }
  local id
  id=$(echo "$resp" | jq -r '.id // empty')
  echo "  [创建] $name  id=$id"
}

# ── 创建三个 Score Config ─────────────────────────────────────────────────────
echo "Langfuse Score Config 初始化"
echo "  目标：$ENDPOINT"
echo ""

create_config "task_completion" '{
  "name": "task_completion",
  "dataType": "NUMERIC",
  "minValue": 0,
  "maxValue": 1,
  "description": "任务完成度：用户是否得到了想要的结果（1=完全满意，0=完全未达到）"
}'

create_config "task_efficiency" '{
  "name": "task_efficiency",
  "dataType": "NUMERIC",
  "minValue": 0,
  "maxValue": 1,
  "description": "任务完成效率：交互轮次少、无工具报错得高分"
}'

create_config "user_signal" '{
  "name": "user_signal",
  "dataType": "CATEGORICAL",
  "categories": [
    {"label": "satisfied",  "value": 1},
    {"label": "redirected", "value": 0.5},
    {"label": "abandoned",  "value": 0}
  ],
  "description": "用户信号：satisfied=满意完成 / redirected=中途转向 / abandoned=放弃"
}'

echo ""
echo "完成。刷新 Langfuse UI → Traces 列表 → Columns，勾选上述三列即可看到指标。"
