#!/usr/bin/env bash
# Author:    维杰（邬晶）
# Email:     wujing03@bilibili.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
# scripts/build.sh — 编译并打包可部署的发布包（mac x86 / darwin-amd64）
# 用法: bash scripts/build.sh [输出文件名]
# 示例: bash scripts/build.sh               → OTTClaw-darwin-amd64-20260313.zip
#       bash scripts/build.sh release.zip    → release.zip
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)
PROJECT=$(basename "$ROOT")
BIN_NAME="OTTClaw"

# ── 目标平台（mac x86） ────────────────────────────────────────
TARGET_OS="darwin"
TARGET_ARCH="amd64"

# ── 输出文件名 ─────────────────────────────────────────────────
DATE=$(date +%Y%m%d)
OUT="${1:-${PROJECT}-${TARGET_OS}-${TARGET_ARCH}-${DATE}.zip}"
[[ "$OUT" != /* ]] && OUT="$ROOT/$OUT"

# ── 临时目录 ──────────────────────────────────────────────────
TMPDIR=$(mktemp -d)
STAGE="$TMPDIR/$PROJECT"
trap 'rm -rf "$TMPDIR"' EXIT

echo "🔨 编译 $BIN_NAME (${TARGET_OS}/${TARGET_ARCH})..."

# ── 1. 编译 Go 二进制 ─────────────────────────────────────────
mkdir -p "$STAGE/bin"
GOOS=$TARGET_OS GOARCH=$TARGET_ARCH \
  go build \
    -trimpath \
    -ldflags="-s -w -X main.BuildDate=${DATE}" \
    -o "$STAGE/bin/$BIN_NAME" \
    .
echo "   ✓ bin/$BIN_NAME"

GOOS=$TARGET_OS GOARCH=$TARGET_ARCH \
  go build \
    -trimpath \
    -ldflags="-s -w" \
    -o "$STAGE/bin/gen-token" \
    ./cmd/gen-token
echo "   ✓ bin/gen-token"

# ── 2. 前端静态文件 ────────────────────────────────────────────
cp -r client "$STAGE/"
echo "   ✓ client/"

# ── 3. 配置文件 ───────────────────────────────────────────────
cp -r config "$STAGE/"
# 重置为初始状态，避免打包当前实例的运行时配置
cp config/bootstrap/ROLE.md "$STAGE/config/ROLE.md"
printf '{\n  "avatar_url": "",\n  "initialized": false\n}\n' > "$STAGE/config/app.json"
echo "   ✓ config/"

# ── 4. 技能目录（不含用户私有技能） ──────────────────────────────
cp -r skills "$STAGE/"
rm -rf "$STAGE/skills/users"
echo "   ✓ skills/"

# ── 5. 浏览器 sidecar（不含 node_modules，接收方 npm install） ─
mkdir -p "$STAGE/browser-server"
cp browser-server/server.js "$STAGE/browser-server/"
cp browser-server/package.json "$STAGE/browser-server/"
cp browser-server/package-lock.json "$STAGE/browser-server/"
echo "   ✓ browser-server/"

# ── 6. 运维脚本（部署专用，不含 go build） ────────────────────
mkdir -p "$STAGE/scripts"
cp scripts/start.sh     "$STAGE/scripts/"
cp scripts/stop.sh      "$STAGE/scripts/"
cp scripts/gen-token.sh "$STAGE/scripts/"
chmod +x "$STAGE/scripts/start.sh" "$STAGE/scripts/stop.sh" "$STAGE/scripts/gen-token.sh"
echo "   ✓ scripts/start.sh  scripts/stop.sh  scripts/gen-token.sh"

# ── 7. 配置示例 ───────────────────────────────────────────────
[ -f .env.example ] && cp .env.example "$STAGE/.env.example" && echo "   ✓ .env.example"

# ── 8. 创建必要的空目录 ────────────────────────────────────────
for dir in data logs run uploads output; do
  mkdir -p "$STAGE/$dir"
  touch "$STAGE/$dir/.gitkeep"
done
echo "   ✓ 空目录: data/ logs/ run/ uploads/ output/"

# ── 9. 清理垃圾文件 ────────────────────────────────────────────
find "$STAGE" -name '.DS_Store' -delete 2>/dev/null || true
find "$STAGE" -name '*.test'   -delete 2>/dev/null || true

# ── 10. 打包 ─────────────────────────────────────────────────
(cd "$TMPDIR" && zip -r -q "$OUT" "$PROJECT" -x "*/node_modules/*" "*/.DS_Store")

SIZE=$(du -h "$OUT" | awk '{print $1}')
echo ""
echo "✅ 完成: $(basename "$OUT") ($SIZE)"
echo ""
echo "部署步骤:"
echo "  1. 上传 $(basename "$OUT") 到服务器并解压："
echo "       unzip $(basename "$OUT")"
echo "       cd $PROJECT"
echo "  2. 复制并填写配置："
echo "       cp .env.example .env  # 填写 LLM_API_KEY 等"
echo "  3. 安装 browser-server 依赖（需要 Node.js）："
echo "       cd browser-server && npm install && cd .."
echo "  4. 启动服务："
echo "       bash scripts/start.sh"
echo "  5. 停止服务："
echo "       bash scripts/stop.sh"
echo "  6. 签发邀请码："
echo "       bash scripts/gen-token.sh invite <user-id> [-n 设备数] [-ttl 有效期]"
