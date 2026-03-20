#!/usr/bin/env bash
# Author:    Vijay
# Email:     hustwujing@163.com
# Date:      2026
# Copyright: Copyright (c) 2026 Vijay
#
# scripts/gen-token.sh — 邀请码 / JWT 签发工具
#
# 用法：
#   bash scripts/gen-token.sh invite <user-id> [-n 设备数] [-ttl 有效期]
#   bash scripts/gen-token.sh token  [user-id] [ttl]
#
# 示例：
#   bash scripts/gen-token.sh invite alice
#   bash scripts/gen-token.sh invite alice -n 3 -ttl 30d
#   bash scripts/gen-token.sh token  alice 24h
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

BIN="$ROOT_DIR/bin/gen-token"

if [ ! -f "$BIN" ]; then
  echo "错误：未找到 $BIN"
  exit 1
fi

exec "$BIN" "$@"
