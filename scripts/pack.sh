#!/usr/bin/env bash
# Author:    Vijay
# Email:     hustwujing@163.com
# Date:      2026
# Copyright: Copyright (c) 2026 Vijay
#
# scripts/pack.sh — 打包发行 zip，去除所有运行时数据和敏感信息
# 用法: bash scripts/pack.sh [输出文件名]
# 示例: bash scripts/pack.sh               → OTTClaw-20260313.zip
#       bash scripts/pack.sh release.zip    → release.zip
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)
PROJECT=$(basename "$ROOT")

# 输出文件名
DATE=$(date +%Y%m%d)
OUT="${1:-${PROJECT}-${DATE}.zip}"
# 确保输出路径为绝对路径
[[ "$OUT" != /* ]] && OUT="$ROOT/$OUT"

# 临时目录
TMPDIR=$(mktemp -d)
STAGE="$TMPDIR/$PROJECT"
trap 'rm -rf "$TMPDIR"' EXIT

echo "📦 打包 $PROJECT → $OUT"

# ── 1. 复制源码（只包含必要文件） ─────────────────────────────
mkdir -p "$STAGE"

# Go 源码
cp main.go go.mod go.sum "$STAGE/"
cp -r internal "$STAGE/"
cp -r cmd "$STAGE/"

# 配置模板（不含真实配置）
cp -r config "$STAGE/"
# 重置 ROLE.md 为 bootstrap 初始向导内容，避免打包当前实例的私有配置
cp config/bootstrap/ROLE.md "$STAGE/config/ROLE.md"
# 重置 app.json 为初始状态，避免打包当前实例的运行时配置
printf '{\n  "avatar_url": "",\n  "initialized": false\n}\n' > "$STAGE/config/app.json"
cp .env.example "$STAGE/.env.example"
cp .gitignore "$STAGE/"

# 前端
cp -r client "$STAGE/"

# 技能（不含用户私有技能）
cp -r skills "$STAGE/"
rm -rf "$STAGE/skills/users"

# 浏览器 sidecar（不含 node_modules，接收方 npm install）
mkdir -p "$STAGE/browser-server"
cp browser-server/server.js "$STAGE/browser-server/"
cp browser-server/package.json "$STAGE/browser-server/"
cp browser-server/package-lock.json "$STAGE/browser-server/"

# 运维脚本
cp -r scripts "$STAGE/"

# 文档
[ -f README.md ] && cp README.md "$STAGE/"

# ── 2. 创建接收方需要的空目录（附 .gitkeep） ─────────────────
for dir in bin data logs run uploads output; do
  mkdir -p "$STAGE/$dir"
  touch "$STAGE/$dir/.gitkeep"
done

# ── 3. 清理垃圾文件 ──────────────────────────────────────────
find "$STAGE" -name '.DS_Store' -delete 2>/dev/null || true
find "$STAGE" -name '*.test' -delete 2>/dev/null || true
find "$STAGE" -type d -name '.claude' -exec rm -rf {} + 2>/dev/null || true
find "$STAGE" -type d -name '.claude-plugin' -exec rm -rf {} + 2>/dev/null || true

# ── 4. 打包 ──────────────────────────────────────────────────
(cd "$TMPDIR" && zip -r -q "$OUT" "$PROJECT" -x "*/.DS_Store" "*/node_modules/*")

SIZE=$(du -h "$OUT" | awk '{print $1}')
echo "✅ 完成: $OUT ($SIZE)"
echo ""
echo "接收方使用步骤:"
echo "  1. unzip $(basename "$OUT")"
echo "  2. cd $PROJECT"
echo "  3. cp .env.example .env  # 填写真实配置"
echo "  4. cd browser-server && npm install && cd .."
echo "  5. go build -o bin/OTTClaw ."
echo "  6. bash scripts/start.sh"
