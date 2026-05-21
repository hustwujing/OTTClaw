#!/usr/bin/env bash
# Author:    Vijay
# Email:     hustwujing@163.com
# Date:      2026
# Copyright: Copyright (c) 2026 Vijay
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
SERVER_PORT="${SERVER_PORT:-$(grep -E '^SERVER_PORT=' "$ROOT_DIR/.env" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')}"
SERVER_PORT="${SERVER_PORT:-8081}"

# ---- 清理占用服务端口的残留进程（不被 PID 文件跟踪的 go run 进程等） ----
_cleanup_port() {
  local pids
  pids=$(lsof -ti :"$SERVER_PORT" 2>/dev/null || true)
  if [ -n "$pids" ]; then
    echo "[stop] 清理占用端口 $SERVER_PORT 的进程：$pids"
    echo "$pids" | xargs kill 2>/dev/null || true
    sleep 1
    # 仍未退出则强杀
    pids=$(lsof -ti :"$SERVER_PORT" 2>/dev/null || true)
    if [ -n "$pids" ]; then
      echo "$pids" | xargs kill -9 2>/dev/null || true
    fi
  fi
}

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
        (cd "$BROWSER_SERVER_DIR" && npm ci --silent 2>/dev/null || npm install --silent)
        echo "[start] browser-server 依赖安装完成"
      fi
      if [ -f "$BROWSER_SERVER_DIR/node_modules/playwright-core/browsers.json" ] \
        && ! ls "$HOME/Library/Caches/ms-playwright/"chromium_headless_shell-*/chrome-headless-shell >/dev/null 2>&1; then
        echo "[start] Chromium 浏览器不存在，正在安装..."
        (cd "$BROWSER_SERVER_DIR" && npx playwright install chromium 2>/dev/null)
        echo "[start] Chromium 浏览器就绪"
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

  # ---- 端口占用检查 ----
  if lsof -ti :"$SERVER_PORT" &>/dev/null; then
    echo "[start] 错误：端口 $SERVER_PORT 已被占用（$(lsof -ti :"$SERVER_PORT" | xargs ps -p 2>/dev/null | tail -n +2 || true)）"
    echo "[start]   请先运行：bash scripts/service.sh stop"
    exit 1
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
  if [ -f "$PID_FILE" ]; then
    PID="$(tr -d '[:space:]' < "$PID_FILE")"
    if kill -0 "$PID" 2>/dev/null; then
      echo "[stop] 发送 SIGTERM 到 PID=$PID..."
      kill "$PID"

      # 等待进程退出（最多 35 秒，覆盖 HTTP server 30s shutdown + bgWg 等待时间）
      for i in $(seq 1 35); do
        if ! kill -0 "$PID" 2>/dev/null; then
          echo "[stop] 服务已停止（等待 ${i}s）"
          break
        fi
        sleep 1
      done

      # 超时后强制杀死
      if kill -0 "$PID" 2>/dev/null; then
        echo "[stop] 进程未在 35s 内退出，发送 SIGKILL..."
        kill -9 "$PID" 2>/dev/null || true
        echo "[stop] 服务已强制终止"
      fi
    else
      echo "[stop] 进程 PID=$PID 已不存在，清理 PID 文件"
    fi
    rm -f "$PID_FILE"
  else
    echo "[stop] PID 文件不存在，尝试按端口清理..."
  fi

  # 兜底：清理任何仍占用服务端口的进程（包括 go run 启动的未跟踪进程）
  _cleanup_port
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
