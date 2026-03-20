#!/bin/bash
# Author:    维杰（邬晶）
# Email:     wujing03@bilibili.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
# install.sh — Detect and install Homebrew
set -euo pipefail

# ── Check if already installed ────────────────────────────────────────────────────
if command -v brew &>/dev/null; then
    echo "Already installed: $(brew --version | head -1)"
    exit 0
fi

# Default paths for Apple Silicon and Intel
for brew_path in /opt/homebrew/bin/brew /usr/local/bin/brew; do
    if [[ -x "$brew_path" ]]; then
        eval "$("$brew_path" shellenv)"
        echo "Already installed (path: $brew_path): $(brew --version | head -1)"
        exit 0
    fi
done

# ── Begin installation ─────────────────────────────────────────────────────────
echo "brew not installed, starting Homebrew installation..."

export NONINTERACTIVE=1
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# ── Verify installation result ──────────────────────────────────────────────────
for brew_path in /opt/homebrew/bin/brew /usr/local/bin/brew; do
    if [[ -x "$brew_path" ]]; then
        eval "$("$brew_path" shellenv)"
        break
    fi
done

if command -v brew &>/dev/null; then
    echo "Installation successful: $(brew --version | head -1)"
else
    echo "ERROR: Installation script completed, but the brew command was not found. Possible causes: insufficient sudo privileges, or brew needs to be added to PATH manually." >&2
    exit 1
fi
