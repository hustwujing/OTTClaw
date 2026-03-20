#!/usr/bin/env bash
# Author:    维杰（邬晶）
# Email:     wujing03@bilibili.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
# scripts/publish.sh — 将 OTTClaw 发布到 GitHub
#
# 用法：
#   bash scripts/publish.sh
#
# 脚本会交互式询问 GitHub Token、仓库名称和可见性，
# 完成以下操作：
#   1. 初始化本地 git 仓库（如尚未初始化）
#   2. 写入 .gitignore（排除运行时数据、个人配置等）
#   3. 用 config/bootstrap/ 模板替换 git index 中的个人配置文件
#   4. 通过 GitHub API 创建远程仓库
#   5. 提交并推送所有代码

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

# ── 颜色输出 ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()    { echo -e "${CYAN}[publish]${NC} $*"; }
success() { echo -e "${GREEN}[publish]${NC} $*"; }
warn()    { echo -e "${YELLOW}[publish]${NC} $*"; }
error()   { echo -e "${RED}[publish]${NC} $*" >&2; exit 1; }

# ── 前置检查 ──────────────────────────────────────────────────────────────────
command -v git  >/dev/null 2>&1 || error "未找到 git，请先安装。"
command -v curl >/dev/null 2>&1 || error "未找到 curl，请先安装。"

# ── 交互式输入 ────────────────────────────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════"
echo "       OTTClaw → GitHub 发布工具"
echo "═══════════════════════════════════════"
echo ""

# GitHub Personal Access Token
read -rsp "GitHub Personal Access Token（输入不显示）: " GH_TOKEN
echo ""
[[ -z "$GH_TOKEN" ]] && error "Token 不能为空。"

# 从 Token 获取用户名
info "正在验证 Token..."
GH_USER=$(curl -sf -H "Authorization: token $GH_TOKEN" https://api.github.com/user \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['login'])" 2>/dev/null) \
  || error "Token 无效或网络异常，请检查后重试。"
success "已验证，用户名：$GH_USER"

# 仓库名称
read -rp "仓库名称 [默认: OTTClaw]: " REPO_NAME
REPO_NAME="${REPO_NAME:-OTTClaw}"

# 可见性
echo "仓库可见性："
echo "  1) public（公开）"
echo "  2) private（私有）"
read -rp "请选择 [默认: 1]: " VIS_CHOICE
if [[ "${VIS_CHOICE:-1}" == "2" ]]; then
  REPO_PRIVATE="true"
  VIS_LABEL="private"
else
  REPO_PRIVATE="false"
  VIS_LABEL="public"
fi

echo ""
info "即将发布：github.com/$GH_USER/$REPO_NAME（$VIS_LABEL）"
read -rp "确认继续？[Y/n]: " CONFIRM
[[ "${CONFIRM:-Y}" =~ ^[Nn] ]] && { info "已取消。"; exit 0; }

# ── Step 1：初始化 git ─────────────────────────────────────────────────────────
echo ""
if [[ ! -d ".git" ]]; then
  info "初始化 git 仓库..."
  git init
  git branch -M main
else
  info "git 仓库已存在，跳过初始化。"
fi

# ── Step 2：写入 .gitignore ────────────────────────────────────────────────────
info "写入 .gitignore..."
cat > .gitignore << 'EOF'
# 本地环境变量（含敏感信息，不提交）
.env

# 编译产物（保留空目录）
bin/*
!bin/.gitkeep

# 运行时数据（保留空目录）
data/*
!data/.gitkeep
run/*
!run/.gitkeep

# 用户上传 / 生成文件（保留空目录）
uploads/*
!uploads/.gitkeep
output/*
!output/.gitkeep

# 日志（保留空目录）
logs/*
!logs/.gitkeep

# Node.js 依赖
browser-server/node_modules/

# Python 虚拟环境
client/.venv/

# 用户自定义技能（个人数据，保留空目录）
skills/users/*
!skills/users/.gitkeep

# Claude Code 工作目录
.claude/

# macOS
.DS_Store
EOF

# ── Step 3：为运行时目录创建 .gitkeep（保留空目录结构）─────────────────────────
info "创建空目录占位文件（.gitkeep）..."
for DIR in bin data run uploads output logs skills/users; do
  mkdir -p "$DIR"
  touch "$DIR/.gitkeep"
  # 若已被追踪的旧内容存在，先清除
  git rm -r --cached "$DIR/" 2>/dev/null || true
  git add "$DIR/.gitkeep"
  success "  $DIR/.gitkeep"
done

# ── Step 4：用 bootstrap 模板替换个人配置文件 ─────────────────────────────────
info "将 config/bootstrap/ 模板写入 git index..."

# 先全量 stage，再对个人配置文件做替换
git add .

for FILE in ROLE.md app.json; do
  BOOTSTRAP="config/bootstrap/$FILE"
  TARGET="config/$FILE"
  if [[ -f "$BOOTSTRAP" ]]; then
    # 将 bootstrap 版本写入 object store，获取 hash
    HASH=$(git hash-object -w "$BOOTSTRAP")
    # 从 index 移除个人版本（不动磁盘文件）
    git rm --cached "$TARGET" 2>/dev/null || true
    # 以 bootstrap 内容注册到 index 中的 config/ 路径
    git update-index --add --cacheinfo "100644,$HASH,$TARGET"
    # 本地文件标记为 skip-worktree，避免被 git 标记为"已修改"
    git update-index --skip-worktree "$TARGET"
    success "  $TARGET → 使用 bootstrap 模板"
  else
    warn "  未找到 $BOOTSTRAP，跳过。"
  fi
done

# 重新 stage（确保其他文件都已加入）
git add .

# ── Step 5：创建 GitHub 仓库 ───────────────────────────────────────────────────
info "在 GitHub 创建仓库 $REPO_NAME..."

REPO_DESC="OpenClaw 的服务器版——让整个团队共享同一套 AI Agent 能力，无需每人单独部署。"
CREATE_RESP=$(curl -sf -X POST \
  -H "Authorization: token $GH_TOKEN" \
  -H "Content-Type: application/json" \
  https://api.github.com/user/repos \
  -d "{\"name\":\"$REPO_NAME\",\"description\":\"$REPO_DESC\",\"private\":$REPO_PRIVATE,\"has_issues\":true,\"has_wiki\":false}" \
  2>/dev/null) || true

REPO_URL=$(echo "$CREATE_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('html_url',''))" 2>/dev/null)
REPO_ERR=$(echo "$CREATE_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('message',''))" 2>/dev/null)

if [[ -n "$REPO_URL" ]]; then
  success "仓库已创建：$REPO_URL"
elif echo "$REPO_ERR" | grep -q "already exists"; then
  warn "仓库已存在，将直接推送到已有仓库。"
  REPO_URL="https://github.com/$GH_USER/$REPO_NAME"
else
  error "创建仓库失败：$REPO_ERR"
fi

# ── Step 6：更新 README 中的仓库链接 ──────────────────────────────────────────
if [[ -f "README.md" ]]; then
  # 替换 clone URL 占位
  sed -i.bak "s|https://github.com/your-org/$REPO_NAME|https://github.com/$GH_USER/$REPO_NAME|g" README.md
  rm -f README.md.bak
  git add README.md
fi

# ── Step 7：提交并推送 ────────────────────────────────────────────────────────
info "提交代码..."
git config user.name  "$GH_USER"
git config user.email "${GH_USER}@users.noreply.github.com"

STAGED=$(git diff --cached --name-only | wc -l | tr -d ' ')
if [[ "$STAGED" -gt 0 ]]; then
  git commit -m "Initial release

OTTClaw — OpenClaw 的服务器版，支持团队多用户共享部署。"
else
  info "没有需要提交的变更，跳过 commit。"
fi

REMOTE_URL="https://${GH_USER}:${GH_TOKEN}@github.com/${GH_USER}/${REPO_NAME}.git"

if git remote get-url origin >/dev/null 2>&1; then
  git remote set-url origin "$REMOTE_URL"
else
  git remote add origin "$REMOTE_URL"
fi

info "推送到 GitHub..."
git branch -M main
git push -u origin main

# ── 完成 ──────────────────────────────────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════"
success "发布完成！"
echo ""
echo "  仓库地址：https://github.com/$GH_USER/$REPO_NAME"
echo ""
echo "  ⚠️  提醒：Token 已用完毕，建议立即吊销并重新生成："
echo "     https://github.com/settings/tokens"
echo "═══════════════════════════════════════"
