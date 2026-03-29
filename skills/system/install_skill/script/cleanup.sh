#!/bin/bash
# Clean up the temporary skill install directory for this session
SESSION_ID="${SKILL_SESSION_ID:-default}"
rm -rf "/tmp/_skill_install_${SESSION_ID}"
