#!/bin/sh
# Author:    维杰（邬晶）
# Email:     wujing03@bilibili.com
# Date:      2026
# Copyright: Copyright (c) 2026 维杰（邬晶）
#
# install_git.sh — Detect the operating system and install Git
set -e

# If already installed, return the version immediately
if command -v git >/dev/null 2>&1; then
  echo "Git is already installed: $(git --version)"
  exit 0
fi

echo "Git not detected, identifying operating system..."

if [ -f /etc/os-release ]; then
  . /etc/os-release
  OS=$ID
elif [ "$(uname)" = "Darwin" ]; then
  OS="macos"
else
  OS="unknown"
fi

echo "Operating system: $OS"

case "$OS" in
  ubuntu|debian|linuxmint)
    apt-get update -qq
    apt-get install -y git
    ;;
  centos|rhel|fedora|rocky|almalinux)
    yum install -y git
    ;;
  alpine)
    apk add --no-cache git
    ;;
  macos)
    if command -v brew >/dev/null 2>&1; then
      brew install git
    else
      echo "Error: Homebrew not found. Please install Homebrew first or install Git manually." >&2
      exit 1
    fi
    ;;
  *)
    echo "Error: Unsupported operating system ($OS). Please install Git manually." >&2
    exit 1
    ;;
esac

echo "Installation complete: $(git --version)"
