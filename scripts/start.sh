#!/usr/bin/env bash
# Author:    Vijay
# Email:     hustwujing@163.com
# Date:      2026
# Copyright: Copyright (c) 2026 Vijay
#
# scripts/start.sh — 启动预编译发布包中的 OTTClaw 服务
# 用法：bash scripts/start.sh
#
# 适用场景：由 build.sh 打包的发布包（bin/ 已含编译好的二进制）
# 开发环境请使用 scripts/service.sh start（会自动 go build）
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

BIN="$ROOT_DIR/bin/OTTClaw"
PID_DIR="$ROOT_DIR/run"
PID_FILE="$PID_DIR/server.pid"
LOG_DIR="${LOG_DIR:-logs}"
LOG_FILE_STDOUT="$LOG_DIR/stdout.log"
BROWSER_SERVER_DIR="$ROOT_DIR/browser-server"

# ---- 二进制检查 ----
if [ ! -f "$BIN" ]; then
  echo "[start] 错误：未找到可执行文件 $BIN"
  echo "[start]   如需从源码编译，请使用：bash scripts/service.sh start"
  exit 1
fi

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

# ---- .env 检查 ----
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

# ---- 启动 ----
mkdir -p "$PID_DIR" "$LOG_DIR"
echo "[start] 启动服务（后台）..."
nohup "$BIN" >> "$LOG_FILE_STDOUT" 2>&1 &
SERVER_PID=$!
echo "$SERVER_PID" > "$PID_FILE"

echo "[start] 服务已启动，PID=$SERVER_PID"
echo "[start] stdout 日志：$LOG_FILE_STDOUT"
echo "[start] PID 文件：$PID_FILE"
