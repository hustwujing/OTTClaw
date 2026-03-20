#!/bin/bash
# Author:    维杰（邬晶）
# Email:     wujing03@bilibili.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
# install.sh — Detect and install Node.js
set -euo pipefail

# ── Prepend common PATH entries ────────────────────────────────────────────────
for brew_path in /opt/homebrew/bin /usr/local/bin; do
    [[ -d "$brew_path" ]] && export PATH="$brew_path:$PATH"
done

# ── Check if node is already installed ───────────────────────────────────────────
if command -v node &>/dev/null; then
    echo "Already installed: $(node --version), npm $(npm --version)"
    exit 0
fi

# ── Preferred path: use Homebrew ──────────────────────────────────────────────
if command -v brew &>/dev/null; then
    echo "Homebrew detected, running brew install node..."
    brew install node
    echo "Installation successful (via Homebrew): node $(node --version), npm $(npm --version)"
    exit 0
fi

# ── Fallback path: use nvm ───────────────────────────────────────────────────
echo "Homebrew not detected, installing Node.js LTS via nvm..."

export NVM_DIR="$HOME/.nvm"

# Install nvm if not already installed
if [[ ! -s "$NVM_DIR/nvm.sh" ]]; then
    curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
fi

# Load nvm
\. "$NVM_DIR/nvm.sh"

# Install LTS version and set as default
nvm install --lts
nvm alias default node

# ── Verify ─────────────────────────────────────────────────────────────
if command -v node &>/dev/null; then
    echo "Installation successful (via nvm): node $(node --version), npm $(npm --version)"
else
    echo "ERROR: Installation completed but the node command is unavailable. Please open a new terminal or check PATH." >&2
    exit 1
fi
