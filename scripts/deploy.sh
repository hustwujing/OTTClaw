#!/usr/bin/env bash
# Author:    维杰（邬晶）
# Email:     wujing03@bilibili.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
# scripts/deploy.sh — 交互式构建并发布到远程 macOS 机器
# 用法: bash scripts/deploy.sh
if [ -z "${BASH_VERSION:-}" ]; then
  echo "请使用 bash 运行此脚本: bash scripts/deploy.sh" >&2
  exit 1
fi

set -euo pipefail

# 先固定 scripts 目录的绝对路径，再 cd 到项目根
SCRIPTS_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPTS_DIR/.."
ROOT=$(pwd)
PROJECT=$(basename "$ROOT")
DATE=$(date +%Y%m%d)

# ── 退出时清理本地 zip（无论成功或失败） ─────────────────────────
ZIP_PATH=""
cleanup() {
  if [ -n "$ZIP_PATH" ] && [ -f "$ZIP_PATH" ]; then
    rm -f "$ZIP_PATH"
    echo -e "\n🧹 已清理本地安装包: $(basename "$ZIP_PATH")"
  fi
}
trap cleanup EXIT

# ── 颜色输出 ───────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}$*${RESET}"; }
success() { echo -e "${GREEN}$*${RESET}"; }
warn()    { echo -e "${YELLOW}$*${RESET}"; }
error()   { echo -e "${RED}$*${RESET}" >&2; }

# ── 检查本地依赖 ───────────────────────────────────────────────
if ! command -v sshpass &>/dev/null; then
  error "❌ 需要 sshpass，请先安装: brew install sshpass"
  exit 1
fi

echo ""
echo -e "${BOLD}╔══════════════════════════════════════╗${RESET}"
echo -e "${BOLD}║        OTTClaw 发布助手              ║${RESET}"
echo -e "${BOLD}╚══════════════════════════════════════╝${RESET}"
echo ""

# ── 1. 选择架构 ────────────────────────────────────────────────
echo "请选择发布架构:"
echo "  1) amd64  — Intel Mac (x86_64)"
echo "  2) arm64  — Apple Silicon (M1/M2/M3)"
echo ""
while true; do
  read -rp "请输入选项 [1/2]: " ARCH_CHOICE </dev/tty
  case "$ARCH_CHOICE" in
    1) ARCH="amd64"; break ;;
    2) ARCH="arm64"; break ;;
    *) warn "   请输入 1 或 2" ;;
  esac
done

# ── 2. 询问目标机器信息 ────────────────────────────────────────
echo ""
info "── 目标机器配置 ─────────────────────────────"
if [ "$ARCH" = "amd64" ]; then
  read -rp  "目标 IP 地址 [192.168.3.125]: "  REMOTE_HOST </dev/tty; REMOTE_HOST="${REMOTE_HOST:-192.168.3.125}"
  read -rp  "用户名       [bili]:           "  REMOTE_USER </dev/tty; REMOTE_USER="${REMOTE_USER:-bili}"
  read -rsp "密码         [2233]:           "  REMOTE_PASS </dev/tty; REMOTE_PASS="${REMOTE_PASS:-2233}"; echo ""
else
  read -rp  "目标 IP 地址 [10.23.2.10]:     " REMOTE_HOST </dev/tty; REMOTE_HOST="${REMOTE_HOST:-10.23.2.10}"
  read -rp  "用户名       [bilibili]:        " REMOTE_USER </dev/tty; REMOTE_USER="${REMOTE_USER:-bilibili}"
  read -rsp "密码         [2233]:           "  REMOTE_PASS </dev/tty; REMOTE_PASS="${REMOTE_PASS:-2233}"; echo ""
fi

REMOTE_DESKTOP="/Users/${REMOTE_USER}/Desktop"
ZIP_NAME="${PROJECT}-darwin-${ARCH}-${DATE}.zip"
ZIP_PATH="${ROOT}/${ZIP_NAME}"

echo ""
info "── 发布参数确认 ─────────────────────────────"
echo "  架构: ${ARCH}"
echo "  目标: ${REMOTE_USER}@${REMOTE_HOST}"
echo "  桌面: ${REMOTE_DESKTOP}"
echo "  包名: ${ZIP_NAME}"
echo ""
read -rp "确认发布? [Y/n]: " CONFIRM </dev/tty
case "${CONFIRM:-Y}" in
  [Yy]*|"") ;;
  *) warn "已取消。"; exit 0 ;;
esac

SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=15"

# ── 预检：SSH 连通性 ──────────────────────────────────────────
echo ""
info "🔗 预检   测试远程连通性..."
if ! sshpass -p "$REMOTE_PASS" ssh $SSH_OPTS \
    "${REMOTE_USER}@${REMOTE_HOST}" "echo ok" > /dev/null 2>&1; then
  error "❌ 无法连接到 ${REMOTE_USER}@${REMOTE_HOST}"
  error "   请检查：IP 是否正确、SSH 是否开启、账号密码是否正确"
  exit 1
fi
success "   ✓ 连通性正常"

# ── Step 1/5  构建 ─────────────────────────────────────────────
echo ""
info "🔨 Step 1/5  构建 ${ARCH} 包..."
bash "$SCRIPTS_DIR/build.sh" "$ARCH"

if [ ! -f "$ZIP_PATH" ]; then
  error "❌ 构建产物不存在: $ZIP_PATH"
  exit 1
fi
success "   ✓ 构建完成: $ZIP_NAME"

# ── Step 2/5  检查 .env 新增配置项（本地交互）─────────────────
echo ""
info "🔍 Step 2/5  检查 .env 新增配置项..."

# 从本地 zip 提取 .env.example
EXAMPLE_CONTENT=$(unzip -p "$ZIP_PATH" "${PROJECT}/.env.example" 2>/dev/null) || true

# 拉取远程现有 .env（不存在则为空）
REMOTE_ENV_CONTENT=$(sshpass -p "$REMOTE_PASS" ssh $SSH_OPTS \
  "${REMOTE_USER}@${REMOTE_HOST}" \
  "cat '${REMOTE_DESKTOP}/OTTClaw/.env' 2>/dev/null" 2>/dev/null) || true

NEW_KEY_COUNT=0
NEW_ENV_CONTENT=""

if [ -z "$EXAMPLE_CONTENT" ]; then
  warn "   未找到 .env.example，跳过配置项检查"
else
  while IFS= read -r line; do
    # 跳过注释行和空行
    [[ "$line" =~ ^[[:space:]]*# ]] && continue
    [[ -z "${line// }" ]]           && continue

    KEY="${line%%=*}"
    KEY="${KEY// /}"
    [[ -z "$KEY" ]] && continue

    # 已在远程 .env 中则跳过
    if echo "$REMOTE_ENV_CONTENT" | grep -qE "^${KEY}[[:space:]]*="; then
      continue
    fi

    # 第一个新 key 时打印提示头
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
    DEFAULT_VAL="${line#*=}"

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

# base64 编码，安全透传到远端（防止值中含 $、\、引号等特殊字符）
NEW_ENV_B64=""
[ -n "$NEW_ENV_CONTENT" ] && NEW_ENV_B64=$(printf '%s' "$NEW_ENV_CONTENT" | base64)

# ── Step 3/5  上传安装包 ───────────────────────────────────────
echo ""
info "📦 Step 3/5  上传安装包到远程桌面..."
sshpass -p "$REMOTE_PASS" scp $SSH_OPTS \
  "$ZIP_PATH" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DESKTOP}/"
success "   ✓ 上传完成"

# ── Step 4/5  远程部署 ─────────────────────────────────────────
echo ""
info "🚀 Step 4/5  远程部署中..."

sshpass -p "$REMOTE_PASS" ssh $SSH_OPTS "${REMOTE_USER}@${REMOTE_HOST}" bash <<EOF
set -eu
DESKTOP="${REMOTE_DESKTOP}"
ZIP_NAME="${ZIP_NAME}"
PROJECT="${PROJECT}"
OTTCLAW_DIR="\$DESKTOP/OTTClaw"
DEP_DIR="\$DESKTOP/OTTClaw_dep/${PROJECT}"
ENV_FILE="\$OTTCLAW_DIR/.env"

# ── 无论成功或失败，退出时清理远端中间文件 ───────────────────
remote_cleanup() {
  rm -rf "\$DESKTOP/OTTClaw_dep"   2>/dev/null || true
  rm -f  "\$DESKTOP/\$ZIP_NAME"    2>/dev/null || true
}
trap remote_cleanup EXIT

# ── 检测是否首次部署 ──────────────────────────────────────────
IS_FIRST="false"
[ ! -d "\$OTTCLAW_DIR" ] && IS_FIRST="true"
[ "\$IS_FIRST" = "true" ] && echo "   ℹ 首次部署，将初始化目录结构"

# ── 停止服务（按端口判断，而非进程名）────────────────────────
echo "   → 检查远程服务状态..."
if lsof -i:8081 -sTCP:LISTEN -t > /dev/null 2>&1; then
  echo "      服务运行中，开始停止..."
  # 优先用 stop.sh，否则直接 kill 占用端口的进程
  if [ -f "\$OTTCLAW_DIR/scripts/stop.sh" ]; then
    cd "\$OTTCLAW_DIR/scripts" && bash stop.sh 2>/dev/null || true
  else
    PIDS=\$(lsof -i:8081 -sTCP:LISTEN -t 2>/dev/null || true)
    [ -n "\$PIDS" ] && kill \$PIDS 2>/dev/null || true
  fi
  # 等待端口释放，最多 10s
  FREED="false"
  for i in \$(seq 1 10); do
    sleep 1
    if ! lsof -i:8081 -sTCP:LISTEN -t > /dev/null 2>&1; then
      FREED="true"; break
    fi
  done
  if [ "\$FREED" = "true" ]; then
    echo "      ✓ 服务已停止，端口已释放"
  else
    echo "      ⚠ 端口 8081 在 10s 内未完全释放，继续部署..."
  fi
else
  echo "      ℹ 端口 8081 空闲，服务未在运行，跳过停止"
fi

# ── 解压安装包 ───────────────────────────────────────────────
echo "   → 解压安装包..."
rm -rf "\$DESKTOP/OTTClaw_dep"
mkdir -p "\$DESKTOP/OTTClaw_dep"
unzip -q "\$DESKTOP/\$ZIP_NAME" -d "\$DESKTOP/OTTClaw_dep"
echo "      ✓ 解压完成"

# ── 同步关键目录 ─────────────────────────────────────────────
echo "   → 同步文件到 OTTClaw..."
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

# skills 单独处理：
#   system/  官方内置技能（白名单）每次替换；白名单之外的仅新增，不覆盖
#   其余顶层目录（非 system/非 users）完整替换
#   users/   永不触碰
if [ -d "\$DEP_DIR/skills" ]; then
  mkdir -p "\$OTTCLAW_DIR/skills"

  # system/：官方内置技能（白名单）每次替换；其余仅新增
  if [ -d "\$DEP_DIR/skills/system" ]; then
    mkdir -p "\$OTTCLAW_DIR/skills/system"
    find "\$DEP_DIR/skills/system" -maxdepth 1 -mindepth 1 -type d | while IFS= read -r src; do
      name="\$(basename "\$src")"
      case "\$name" in
        bootstrap|feishu_setup|find_skill|humanizer_zh|install_brew|install_git|\
        install_nodejs|install_python|mermaid_diagram|skill_creator|summarize|\
        tavily_best_practices|tavily_cli|tavily_crawl|tavily_extract|tavily_map|\
        tavily_research|tavily_search|unzip_file|wecom_setup)
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

  # 其余顶层目录（非 system、非 users）：完整替换
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

# browser-server 单独处理：保留 node_modules 和 package-lock.json
if [ -d "\$DEP_DIR/browser-server" ]; then
  mkdir -p "\$OTTCLAW_DIR/browser-server"
  find "\$DEP_DIR/browser-server" -maxdepth 1 -mindepth 1 \
    ! -name "node_modules" ! -name "package-lock.json" | while IFS= read -r src; do
    cp -r "\$src" "\$OTTCLAW_DIR/browser-server/"
  done
  echo "      ✓ browser-server/ (node_modules & package-lock.json 已保留)"
else
  echo "      ⚠ 跳过: browser-server/ (安装包中不存在)"
fi

# ── 同步 config 目录 ─────────────────────────────────────────
# ROLE.md / app.json：用户自定义内容，仅首次部署时写入
# 其余文件：随版本更新，每次覆盖
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

# ── 确保运行时目录存在（首次部署时创建）─────────────────────
for dir in data logs run uploads output; do
  mkdir -p "\$OTTCLAW_DIR/\$dir"
done
echo "      ✓ 运行时目录就绪 (data/ logs/ run/ uploads/ output/)"

# ── 确保脚本可执行 ───────────────────────────────────────────
chmod +x "\$OTTCLAW_DIR/scripts/"*.sh 2>/dev/null || true

# ── 初始化 .env（首次部署时创建空文件）──────────────────────
if [ ! -f "\$ENV_FILE" ]; then
  touch "\$ENV_FILE"
  echo "      ℹ 已初始化空 .env 文件"
fi

# ── 写入新增配置项 ───────────────────────────────────────────
_B64="${NEW_ENV_B64}"
_CNT="${NEW_KEY_COUNT}"
if [ -n "\$_B64" ]; then
  echo "   → 写入 \$_CNT 个新增配置项到 .env..."
  # 保证 .env 末尾有换行，再追加，防止贴到上一行
  [ -s "\$ENV_FILE" ] && echo "" >> "\$ENV_FILE"
  # base64 解码：用临时文件防止 -d/-D 差异导致双写
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

# ── browser-server 依赖检查 ──────────────────────────────────
BS_DIR="\$OTTCLAW_DIR/browser-server"
if [ -f "\$BS_DIR/package.json" ] && [ ! -d "\$BS_DIR/node_modules" ]; then
  echo "   → node_modules 不存在，执行 npm install..."
  if command -v npm > /dev/null 2>&1; then
    cd "\$BS_DIR" && npm install --quiet 2>/dev/null
    echo "      ✓ 依赖安装完成"
  else
    echo "      ⚠ npm 未找到，请手动执行: cd \$BS_DIR && npm install"
  fi
fi

# ── 启动服务 ─────────────────────────────────────────────────
echo "   → 启动服务..."
cd "\$OTTCLAW_DIR/scripts" && bash start.sh

# ── 健康检查（重试循环，最多等 20s）─────────────────────────
echo "   → 等待服务就绪（最多 20s）..."
OK="false"
for i in \$(seq 1 10); do
  sleep 2
  if curl -sf --max-time 3 http://127.0.0.1:8081 > /dev/null 2>&1; then
    OK="true"; break
  fi
  echo "      [\$i/10] 尚未响应，继续等待..."
done

if [ "\$OK" = "true" ]; then
  echo "✅ 服务启动成功"
  echo ""
  echo "   本机访问地址:   http://127.0.0.1:8081"
  echo "   局域网访问地址: http://${REMOTE_HOST}:8081"
else
  echo "❌ 服务在 20s 内未就绪"
  echo "   请登录远程机器排查: tail -50 \$OTTCLAW_DIR/logs/*.log"
  exit 1
fi
EOF

# ── Step 5/5  完成 ─────────────────────────────────────────────
echo ""
success "🎉 Step 5/5  发布完成！"
echo ""
echo -e "   访问地址: ${BOLD}http://${REMOTE_HOST}:8081${RESET}"
echo ""
