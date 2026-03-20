#!/usr/bin/env bash
# Author:    Vijay
# Email:     hustwujing@163.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
# scripts/stop.sh — 停止 OTTClaw 服务
# 用法：bash scripts/stop.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

PID_FILE="$ROOT_DIR/run/server.pid"

# ---- 清理残留的 browser-server 子进程 ----
_cleanup_browser_server() {
  local pids
  pids=$(pgrep -f "node.*browser-server/server\.js" 2>/dev/null || true)
  if [ -n "$pids" ]; then
    echo "[stop] 清理残留 browser-server 进程：$pids"
    echo "$pids" | xargs kill 2>/dev/null || true
  fi
}

if [ ! -f "$PID_FILE" ]; then
  echo "[stop] PID 文件不存在，服务可能未在运行：$PID_FILE"
  exit 0
fi

PID="$(tr -d '[:space:]' < "$PID_FILE")"

if ! kill -0 "$PID" 2>/dev/null; then
  echo "[stop] 进程 PID=$PID 已不存在，清理 PID 文件"
  rm -f "$PID_FILE"
  exit 0
fi

echo "[stop] 发送 SIGTERM 到 PID=$PID..."
kill "$PID"

# 等待进程退出（最多 10 秒）
for i in $(seq 1 10); do
  if ! kill -0 "$PID" 2>/dev/null; then
    rm -f "$PID_FILE"
    echo "[stop] 服务已停止（等待 ${i}s）"
    _cleanup_browser_server
    exit 0
  fi
  sleep 1
done

# 超时后强制杀死
echo "[stop] 进程未在 10s 内退出，发送 SIGKILL..."
kill -9 "$PID" 2>/dev/null || true
rm -f "$PID_FILE"
echo "[stop] 服务已强制终止"
_cleanup_browser_server
