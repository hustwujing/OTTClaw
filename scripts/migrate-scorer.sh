#!/usr/bin/env bash
# Author:    Vijay
# Email:     hustwujing@163.com
# Date:      2026
# Copyright: Copyright (c) 2026 Vijay
#
# scripts/migrate-scorer.sh — Langfuse scorer 游标初始化迁移
#
# 上线 Langfuse Task Unit 评估功能后运行一次，将存量 session 的
# langfuse_cursor_msg_id / last_origin_msg_at 初始化为各自最后一条
# origin_session_messages 的 id / created_at，防止历史消息被重复评分。
#
# 幂等：已初始化（cursor > 0）的 session 不会被修改，可安全重复执行。
#
# 用法：
#   bash scripts/migrate-scorer.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

echo "=== Langfuse scorer 游标迁移 ==="

BIN="$ROOT_DIR/bin/migrate-scorer"
if [ -f "$BIN" ]; then
  "$BIN"
else
  go run cmd/migrate-scorer/main.go
fi

echo "=== 迁移完成 ==="
