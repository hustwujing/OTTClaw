#!/bin/bash
# Clean up the temporary skill install directory for this user
USER_ID="${SKILL_USER_ID:-default}"
TMP_BASE="${AGENT_TMP_DIR:-$(python3 -c "import tempfile; print(tempfile.gettempdir())" 2>/dev/null || echo "/tmp")}"
rm -rf "${TMP_BASE}/_skill_install_${USER_ID}"
