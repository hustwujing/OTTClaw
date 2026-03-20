#!/bin/bash
# Author:    Vijay
# Email:     hustwujing@163.com
# Date:      2026
# Copyright: Copyright (c) 2026 Vijay
#
# install.sh — Detect and install Python 3
set -euo pipefail

# ── Prepend common PATH entries ────────────────────────────────────────────────
for dir in /opt/homebrew/bin /usr/local/bin "$HOME/.pyenv/bin"; do
    [[ -d "$dir" ]] && export PATH="$dir:$PATH"
done

# ── Check if python3 is already installed ───────────────────────────────────────
if command -v python3 &>/dev/null; then
    PY_VER=$(python3 --version)
    PIP_VER=$(pip3 --version 2>/dev/null | awk '{print $1" "$2}' || echo "pip3 not found")
    echo "Already installed: $PY_VER, $PIP_VER"
    exit 0
fi

# ── Preferred path: use Homebrew ──────────────────────────────────────────────
if command -v brew &>/dev/null; then
    echo "Homebrew detected, running brew install python..."
    brew install python
    echo "Installation successful (via Homebrew): $(python3 --version), $(pip3 --version | awk '{print $1" "$2}')"
    exit 0
fi

# ── Fallback path: use pyenv ─────────────────────────────────────────────────
echo "Homebrew not detected, installing Python via pyenv..."

export PYENV_ROOT="$HOME/.pyenv"

# Install pyenv if not already installed
if [[ ! -d "$PYENV_ROOT" ]]; then
    curl -fsSL https://pyenv.run | bash
fi

export PATH="$PYENV_ROOT/bin:$PATH"
eval "$(pyenv init -)"

# Get the latest stable version number (3.x.x, excluding dev/rc/b versions)
LATEST=$(pyenv install --list \
    | grep -E '^\s+3\.[0-9]+\.[0-9]+$' \
    | tail -1 \
    | tr -d ' ')

echo "Installing Python $LATEST..."
pyenv install "$LATEST"
pyenv global "$LATEST"

# ── Verify ─────────────────────────────────────────────────────────────
if command -v python3 &>/dev/null; then
    echo "Installation successful (via pyenv): $(python3 --version), $(pip3 --version | awk '{print $1" "$2}')"
else
    echo "ERROR: Installation completed but the python3 command is unavailable. Please open a new terminal or check PATH." >&2
    exit 1
fi
