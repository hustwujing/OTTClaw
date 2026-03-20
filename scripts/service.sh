#!/usr/bin/env bash
# Author:    Vijay
# Email:     hustwujing@163.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
# scripts/service.sh — OTTClaw 服务管理脚本（启动 / 停止）
# 用法：
#   bash scripts/service.sh start   — 构建并在后台启动服务
#   bash scripts/service.sh stop    — 停止后台运行的服务
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

BIN_DIR="$ROOT_DIR/bin"
BIN="$BIN_DIR/OTTClaw"
PID_DIR="$ROOT_DIR/run"
PID_FILE="$PID_DIR/server.pid"
LOG_DIR="${LOG_DIR:-logs}"
LOG_FILE_STDOUT="$LOG_DIR/stdout.log"
BROWSER_SERVER_DIR="$ROOT_DIR/browser-server"

# ---- 清理残留的 browser-server 子进程 ----
_cleanup_browser_server() {
  local pids
  pids=$(pgrep -f "node.*browser-server/server\.js" 2>/dev/null || true)
  if [ -n "$pids" ]; then
    echo "[stop] 清理残留 browser-server 进程：$pids"
    echo "$pids" | xargs kill 2>/dev/null || true
  fi
}

# ================================================================
do_start() {
  # ---- 环境检查 ----
  if ! command -v go &>/dev/null; then
    echo "[start] 错误：未找到 go 命令，请先安装 Go（https://go.dev/dl/）"
    exit 1
  fi
  echo "[start] Go 版本：$(go version)"

  # ---- Node.js 检查（浏览器自动化功能） ----
  if command -v node &>/dev/null; then
    echo "[start] Node.js 版本：$(node --version)"
    if [ -f "$BROWSER_SERVER_DIR/package.json" ]; then
      if [ ! -d "$BROWSER_SERVER_DIR/node_modules" ]; then
        echo "[start] 安装 browser-server 依赖..."
        (cd "$BROWSER_SERVER_DIR" && npm install --silent)
        echo "[start] browser-server 依赖安装完成"
      fi
    fi
  else
    echo "[start] 警告：未找到 node 命令，浏览器自动化功能不可用"
  fi

  if [ ! -f "$ROOT_DIR/.env" ]; then
    echo "[start] 警告：未找到 .env 文件，将使用默认配置（LLM_API_KEY 等可能为空）"
    echo "[start]   参考 .env.example 创建：cp .env.example .env"
  fi

  # ---- ROLE.md 检查：丢失时从备份自动恢复 ----
  ROLE_MD="$ROOT_DIR/config/ROLE.md"
  ROLE_BACKUP="$ROOT_DIR/config/bootstrap/ROLE.md"
  if [ ! -f "$ROLE_MD" ]; then
    if [ -f "$ROLE_BACKUP" ]; then
      cp "$ROLE_BACKUP" "$ROLE_MD"
      echo "[start] config/ROLE.md 不存在，已从备份自动恢复：config/bootstrap/ROLE.md"
    else
      echo "[start] 警告：config/ROLE.md 和备份均不存在，服务可能无法正常启动"
    fi
  fi

  # ---- 检查是否已在运行 ----
  if [ -f "$PID_FILE" ]; then
    OLD_PID="$(tr -d '[:space:]' < "$PID_FILE")"
    if [ -n "$OLD_PID" ] && kill -0 "$OLD_PID" 2>/dev/null; then
      echo "[start] OTTClaw 已在运行，PID=$OLD_PID"
      exit 0
    else
      echo "[start] 发现过期 PID 文件（PID=${OLD_PID:-空}），清理后重新启动"
      rm -f "$PID_FILE"
    fi
  fi

  # ---- 编译 ----
  echo "[start] 正在编译..."
  mkdir -p "$BIN_DIR"
  go build -o "$BIN" .
  echo "[start] 编译完成：$BIN"

  # ---- 启动 ----
  mkdir -p "$PID_DIR" "$LOG_DIR"
  echo "[start] 启动服务（后台）..."
  nohup "$BIN" >> "$LOG_FILE_STDOUT" 2>&1 &
  SERVER_PID=$!
  echo "$SERVER_PID" > "$PID_FILE"

  echo "[start] 服务已启动，PID=$SERVER_PID"
  echo "[start] stdout 日志：$LOG_FILE_STDOUT"
  echo "[start] PID 文件：$PID_FILE"
}

# ================================================================
do_stop() {
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
}

# ================================================================
case "${1:-}" in
  start) do_start ;;
  stop)  do_stop  ;;
  *)
    echo "用法：bash scripts/service.sh {start|stop}"
    exit 1
    ;;
esac
