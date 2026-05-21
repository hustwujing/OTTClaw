#!/usr/bin/env bash
# Author:    维杰（邬晶）
# Email:     wujing03@bilibili.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
# scripts/deploy_xiaozhazha.sh — 构建并发布到 xiaozhazha (10.23.182.161)
# 用法: bash scripts/deploy_xiaozhazha.sh
if [ -z "${BASH_VERSION:-}" ]; then
  echo "请使用 bash 运行此脚本: bash scripts/deploy_xiaozhazha.sh" >&2
  exit 1
fi

set -euo pipefail

SCRIPTS_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPTS_DIR/.."
ROOT=$(pwd)
PROJECT=$(basename "$ROOT")
DATE=$(date +%Y%m%d)

# ── 硬编码目标机器配置 ───────────────────────────────────────
ARCH="arm64"
REMOTE_HOST="10.23.182.161"
REMOTE_USER="bilibili"
REMOTE_PASS="2233"
REMOTE_BASE="/Users/${REMOTE_USER}"
TARGET_DIR="OTTClaw_xiaozhazha"
SERVICE_PORT="8087"

ZIP_NAME="${PROJECT}-darwin-${ARCH}-${DATE}.zip"
ZIP_PATH="${ROOT}/${ZIP_NAME}"

# ── 退出时清理本地 zip ───────────────────────────────────────
cleanup() {
  if [ -n "$ZIP_PATH" ] && [ -f "$ZIP_PATH" ]; then
    rm -f "$ZIP_PATH"
    echo -e "\n🧹 已清理本地安装包: $(basename "$ZIP_PATH")"
  fi
}
trap cleanup EXIT

# ── 颜色输出 ─────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}$*${RESET}"; }
success() { echo -e "${GREEN}$*${RESET}"; }
warn()    { echo -e "${YELLOW}$*${RESET}"; }
error()   { echo -e "${RED}$*${RESET}" >&2; }

# ── 检查本地依赖 ─────────────────────────────────────────────
if ! command -v sshpass &>/dev/null; then
  error "❌ 需要 sshpass，请先安装: brew install sshpass"
  exit 1
fi

echo ""
echo -e "${BOLD}╔══════════════════════════════════════╗${RESET}"
echo -e "${BOLD}║    OTTClaw 发布助手 → xiaozhazha    ║${RESET}"
echo -e "${BOLD}╚══════════════════════════════════════╝${RESET}"
echo ""
echo "  架构: ${ARCH}"
echo "  目标: ${REMOTE_USER}@${REMOTE_HOST}"
echo "  目录: ${REMOTE_BASE}/${TARGET_DIR}"
echo "  包名: ${ZIP_NAME}"
echo ""
read -rp "确认发布? [Y/n]: " CONFIRM </dev/tty
case "${CONFIRM:-Y}" in
  [Yy]*|"") ;;
  *) warn "已取消。"; exit 0 ;;
esac

SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=15 -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -o IPQoS=throughput"
BW_LIMIT_KBPS=20480

# ── 预检：SSH 连通性 ────────────────────────────────────────
echo ""
info "🔗 预检   测试远程连通性..."
if ! sshpass -p "$REMOTE_PASS" ssh $SSH_OPTS \
    "${REMOTE_USER}@${REMOTE_HOST}" "echo ok" > /dev/null 2>&1; then
  error "❌ 无法连接到 ${REMOTE_USER}@${REMOTE_HOST}"
  error "   请检查：IP 是否正确、SSH 是否开启、账号密码是否正确"
  exit 1
fi
success "   ✓ 连通性正常"

# ── Step 1/5  构建 ───────────────────────────────────────────
echo ""
info "🔨 Step 1/5  构建 ${ARCH} 包..."
# 确保用 Node.js v24 编译（匹配远程版本），否则原生模块 ABI 不兼容
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh" && nvm use 24 2>/dev/null || true
bash "$SCRIPTS_DIR/build.sh" "$ARCH"

if [ ! -f "$ZIP_PATH" ]; then
  error "❌ 构建产物不存在: $ZIP_PATH"
  exit 1
fi
success "   ✓ 构建完成: $ZIP_NAME"

# ── Step 2/5  检查 .env 新增配置项 ──────────────────────────
echo ""
info "🔍 Step 2/5  检查 .env 新增配置项..."

EXAMPLE_CONTENT=$(unzip -p "$ZIP_PATH" "${PROJECT}/.env.example" 2>/dev/null) || true

# 读取本地 .env 作为默认值来源
LOCAL_ENV_CONTENT=""
if [ -f "$ROOT/.env" ]; then
  LOCAL_ENV_CONTENT=$(cat "$ROOT/.env")
fi

REMOTE_ENV_CONTENT=$(sshpass -p "$REMOTE_PASS" ssh $SSH_OPTS \
  "${REMOTE_USER}@${REMOTE_HOST}" \
  "cat '${REMOTE_BASE}/${TARGET_DIR}/.env' 2>/dev/null" 2>/dev/null) || true

NEW_KEY_COUNT=0
NEW_ENV_CONTENT=""

# 从本地 .env 按 key 取值
get_local_val() {
  echo "$LOCAL_ENV_CONTENT" | grep -E "^${1}[[:space:]]*=" | head -1 | cut -d'=' -f2-
}

if [ -z "$EXAMPLE_CONTENT" ]; then
  warn "   未找到 .env.example，跳过配置项检查"
else
  while IFS= read -r line; do
    [[ "$line" =~ ^[[:space:]]*# ]] && continue
    [[ -z "${line// }" ]]           && continue

    KEY="${line%%=*}"
    KEY="${KEY// /}"
    [[ -z "$KEY" ]] && continue

    if echo "$REMOTE_ENV_CONTENT" | grep -qE "^${KEY}[[:space:]]*="; then
      continue
    fi

    if [ "$NEW_KEY_COUNT" -eq 0 ]; then
      echo ""
      if [ -z "$REMOTE_ENV_CONTENT" ]; then
        warn "   远程尚无 .env（首次部署），以下是全部待配置项："
      else
        warn "   发现新增配置项，请逐一确认："
      fi
      warn "   （直接回车使用默认值）"
      echo ""
    fi

    NEW_KEY_COUNT=$(( NEW_KEY_COUNT + 1 ))

    # 默认值优先级：本地 .env > .env.example
    DEFAULT_VAL="${line#*=}"
    LOCAL_VAL="$(get_local_val "$KEY")"
    [ -n "$LOCAL_VAL" ] && DEFAULT_VAL="$LOCAL_VAL"

    read -rp "   ${KEY}=${DEFAULT_VAL} → " USER_VAL </dev/tty
    USER_VAL="${USER_VAL:-$DEFAULT_VAL}"
    NEW_ENV_CONTENT+="${KEY}=${USER_VAL}"$'\n'
  done <<< "$EXAMPLE_CONTENT"

  if [ "$NEW_KEY_COUNT" -eq 0 ]; then
    success "   ✓ 无新增配置项，.env 已是最新"
  else
    echo ""
    success "   ✓ 确认了 ${NEW_KEY_COUNT} 个新增配置项"
  fi
fi

NEW_ENV_B64=""
[ -n "$NEW_ENV_CONTENT" ] && NEW_ENV_B64=$(printf '%s' "$NEW_ENV_CONTENT" | base64)

# ── Step 3/5  上传安装包 ─────────────────────────────────────
echo ""
info "📦 Step 3/5  上传安装包到远程..."
sshpass -p "$REMOTE_PASS" rsync -av --progress --bwlimit=$BW_LIMIT_KBPS \
  -e "ssh $SSH_OPTS" \
  "$ZIP_PATH" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_BASE}/"
success "   ✓ 上传完成"

# ── Step 4/5  远程部署 ───────────────────────────────────────
echo ""
info "🚀 Step 4/5  远程部署中..."

sshpass -p "$REMOTE_PASS" ssh $SSH_OPTS "${REMOTE_USER}@${REMOTE_HOST}" bash <<EOF
set -eu

[ -s "\$HOME/.nvm/nvm.sh" ] && . "\$HOME/.nvm/nvm.sh" 2>/dev/null || true
export PATH="/usr/local/bin:/opt/homebrew/bin:/opt/homebrew/opt/node/bin:\$PATH"

BASE="${REMOTE_BASE}"
ZIP_NAME="${ZIP_NAME}"
PROJECT="${PROJECT}"
TARGET_DIR="${TARGET_DIR}"
OTTCLAW_DIR="\$BASE/\$TARGET_DIR"
DEP_DIR="\$BASE/\${TARGET_DIR}_dep/\$PROJECT"
ENV_FILE="\$OTTCLAW_DIR/.env"

remote_cleanup() {
  rm -rf "\$BASE/\${TARGET_DIR}_dep"  2>/dev/null || true
  rm -f  "\$BASE/\$ZIP_NAME"          2>/dev/null || true
}
trap remote_cleanup EXIT

IS_FIRST="false"
[ ! -d "\$OTTCLAW_DIR" ] && IS_FIRST="true"
[ "\$IS_FIRST" = "true" ] && echo "   ℹ 首次部署，将初始化目录结构"

echo "   → 检查远程服务状态..."
if lsof -i:${SERVICE_PORT} -sTCP:LISTEN -t > /dev/null 2>&1; then
  echo "      服务运行中，开始停止..."
  if [ -f "\$OTTCLAW_DIR/scripts/stop.sh" ]; then
    cd "\$OTTCLAW_DIR/scripts" && bash stop.sh 2>/dev/null || true
  else
    PIDS=\$(lsof -i:${SERVICE_PORT} -sTCP:LISTEN -t 2>/dev/null || true)
    [ -n "\$PIDS" ] && kill \$PIDS 2>/dev/null || true
  fi
  FREED="false"
  for i in \$(seq 1 10); do
    sleep 1
    if ! lsof -i:${SERVICE_PORT} -sTCP:LISTEN -t > /dev/null 2>&1; then
      FREED="true"; break
    fi
  done
  if [ "\$FREED" = "true" ]; then
    echo "      ✓ 服务已停止，端口已释放"
  else
    echo "      ⚠ 端口 ${SERVICE_PORT} 在 10s 内未完全释放，继续部署..."
  fi
else
  echo "      ℹ 端口 ${SERVICE_PORT} 空闲，服务未在运行，跳过停止"
fi

echo "   → 解压安装包..."
rm -rf "\$BASE/\${TARGET_DIR}_dep"
mkdir -p "\$BASE/\${TARGET_DIR}_dep"
unzip -q "\$BASE/\$ZIP_NAME" -d "\$BASE/\${TARGET_DIR}_dep"
echo "      ✓ 解压完成"

echo "   → 同步文件到 \$TARGET_DIR..."
mkdir -p "\$OTTCLAW_DIR"
for dir in bin client scripts; do
  if [ -d "\$DEP_DIR/\$dir" ]; then
    rm -rf "\$OTTCLAW_DIR/\$dir"
    cp -r "\$DEP_DIR/\$dir" "\$OTTCLAW_DIR/\$dir"
    echo "      ✓ \$dir/"
  else
    echo "      ⚠ 跳过: \$dir/ (安装包中不存在)"
  fi
done

if [ -d "\$DEP_DIR/skills" ]; then
  mkdir -p "\$OTTCLAW_DIR/skills"

  if [ -d "\$DEP_DIR/skills/system" ]; then
    mkdir -p "\$OTTCLAW_DIR/skills/system"
    find "\$DEP_DIR/skills/system" -maxdepth 1 -mindepth 1 -type d | while IFS= read -r src; do
      name="\$(basename "\$src")"
      case "\$name" in
        bootstrap|feishu_setup|find_skill|humanizer_zh|install_brew|install_git|\
        install_nodejs|install_python|mermaid_diagram|skill_creator|install_skill|summarize|\
        unzip_file|wecom_setup)
          rm -rf "\$OTTCLAW_DIR/skills/system/\$name"
          cp -r "\$src" "\$OTTCLAW_DIR/skills/system/\$name"
          echo "      ✓ skills/system/\$name/ (已更新)"
          ;;
        *)
          if [ ! -d "\$OTTCLAW_DIR/skills/system/\$name" ]; then
            cp -r "\$src" "\$OTTCLAW_DIR/skills/system/\$name"
            echo "      ✓ skills/system/\$name/ (新增)"
          else
            echo "      - skills/system/\$name/ (非内置，跳过)"
          fi
          ;;
      esac
    done
  fi

  find "\$DEP_DIR/skills" -maxdepth 1 -mindepth 1 ! -name "system" ! -name "users" | while IFS= read -r src; do
    name="\$(basename "\$src")"
    rm -rf "\$OTTCLAW_DIR/skills/\$name"
    cp -r "\$src" "\$OTTCLAW_DIR/skills/\$name"
    echo "      ✓ skills/\$name/"
  done

  mkdir -p "\$OTTCLAW_DIR/skills/users"
  echo "      ✓ skills/ (system/ 仅新增，users/ 已保留)"
else
  echo "      ⚠ 跳过: skills/ (安装包中不存在)"
fi

if [ -d "\$DEP_DIR/browser-server" ]; then
  mkdir -p "\$OTTCLAW_DIR/browser-server"
  find "\$DEP_DIR/browser-server" -maxdepth 1 -mindepth 1 \
    ! -name ".browsers" ! -name "package-lock.json" | while IFS= read -r src; do
    cp -r "\$src" "\$OTTCLAW_DIR/browser-server/"
  done
  echo "      ✓ browser-server/ (package-lock.json 已保留)"

  if [ -d "\$DEP_DIR/browser-server/.browsers/ms-playwright" ]; then
    echo "   → 部署 Chromium 浏览器缓存..."
    mkdir -p "\$HOME/Library/Caches"
    rm -rf "\$HOME/Library/Caches/ms-playwright"
    cp -r "\$DEP_DIR/browser-server/.browsers/ms-playwright" "\$HOME/Library/Caches/"
    echo "      ✓ 浏览器缓存已安装"
  fi
else
  echo "      ⚠ 跳过: browser-server/ (安装包中不存在)"
fi

echo "   → 同步 config 目录..."
mkdir -p "\$OTTCLAW_DIR/config"
if [ -d "\$DEP_DIR/config" ]; then
  find "\$DEP_DIR/config" -type f | while IFS= read -r src; do
    rel="\${src#\$DEP_DIR/config/}"
    dst="\$OTTCLAW_DIR/config/\$rel"
    base="\$(basename "\$src")"
    mkdir -p "\$(dirname "\$dst")"
    if [ "\$base" = "ROLE.md" ] || [ "\$base" = "app.json" ]; then
      if [ ! -f "\$dst" ]; then
        cp "\$src" "\$dst"
        echo "      ✓ config/\$rel (首次写入)"
      fi
    else
      cp "\$src" "\$dst"
      echo "      ✓ config/\$rel"
    fi
  done
else
  echo "      ⚠ 安装包中无 config 目录，跳过"
fi

for dir in data logs run uploads output; do
  mkdir -p "\$OTTCLAW_DIR/\$dir"
done
echo "      ✓ 运行时目录就绪 (data/ logs/ run/ uploads/ output/)"

chmod +x "\$OTTCLAW_DIR/scripts/"*.sh 2>/dev/null || true

if [ ! -f "\$ENV_FILE" ]; then
  touch "\$ENV_FILE"
  echo "      ℹ 已初始化空 .env 文件"
fi

_B64="${NEW_ENV_B64}"
_CNT="${NEW_KEY_COUNT}"
if [ -n "\$_B64" ]; then
  echo "   → 写入 \$_CNT 个新增配置项到 .env..."
  [ -s "\$ENV_FILE" ] && echo "" >> "\$ENV_FILE"
  _TMP=\$(mktemp)
  if printf '%s' "\$_B64" | base64 -d > "\$_TMP" 2>/dev/null; then
    :
  else
    printf '%s' "\$_B64" | base64 -D > "\$_TMP"
  fi
  cat "\$_TMP" >> "\$ENV_FILE"
  rm -f "\$_TMP"
  echo "      ✓ .env 已更新"
else
  echo "   → .env 无需更新"
fi

echo "   → browser-server 依赖 + Chromium 浏览器已随安装包发布，跳过安装"

echo "   → 确保 SERVER_PORT 配置正确..."
if ! grep -qE '^SERVER_PORT[[:space:]]*=' "\$ENV_FILE" 2>/dev/null; then
  echo "SERVER_PORT=${SERVICE_PORT}" >> "\$ENV_FILE"
  echo "      ✓ 已写入 SERVER_PORT=${SERVICE_PORT}"
else
  echo "      ✓ SERVER_PORT 已存在，保持原值"
fi

echo "   → 启动服务..."
cd "\$OTTCLAW_DIR/scripts" && bash start.sh

echo "   → 等待服务就绪（最多 20s）..."
OK="false"
for i in \$(seq 1 10); do
  sleep 2
  if curl -sf --max-time 3 http://127.0.0.1:${SERVICE_PORT} > /dev/null 2>&1; then
    OK="true"; break
  fi
  echo "      [\$i/10] 尚未响应，继续等待..."
done

if [ "\$OK" = "true" ]; then
  echo "✅ 服务启动成功"
  echo ""
  echo "   本机访问地址:   http://127.0.0.1:${SERVICE_PORT}"
  echo "   局域网访问地址: http://${REMOTE_HOST}:${SERVICE_PORT}"
else
  echo "❌ 服务在 20s 内未就绪"
  echo "   请登录远程机器排查: tail -50 \$OTTCLAW_DIR/logs/*.log"
  exit 1
fi
EOF

# ── Step 5/5  完成 ───────────────────────────────────────────
echo ""
success "🎉 Step 5/5  发布完成！"
echo ""
echo -e "   访问地址: ${BOLD}http://${REMOTE_HOST}:${SERVICE_PORT}${RESET}"
echo ""
