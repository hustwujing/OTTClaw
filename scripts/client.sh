#!/usr/bin/env bash
# Author:    维杰（邬晶）
# Email:     wujing03@bilibili.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
# scripts/client.sh — 启动 Python 控制台客户端
# 用法：在项目根目录执行 bash scripts/client.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CLIENT_DIR="$ROOT_DIR/client"
VENV_DIR="$CLIENT_DIR/.venv"

cd "$CLIENT_DIR"

# ---- 创建虚拟环境（首次） ----
if [ ! -d "$VENV_DIR" ]; then
  echo "[client] 创建 Python 虚拟环境..."
  python3 -m venv "$VENV_DIR"
fi

# ---- 激活虚拟环境 ----
# shellcheck disable=SC1091
source "$VENV_DIR/bin/activate"

# ---- 安装依赖（依赖有变更时自动更新） ----
pip install -q -r requirements.txt

# ---- 启动客户端 ----
echo "[client] 启动控制台客户端..."
python3 client.py "$@"
