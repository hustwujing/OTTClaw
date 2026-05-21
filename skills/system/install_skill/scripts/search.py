#!/usr/bin/env python3
"""Search for skills in the open skills.sh ecosystem.
Usage: search.py "<keywords>"
Outputs skill results, or a line starting with UNAVAILABLE: if search cannot proceed.
"""
import shutil
import subprocess
import sys

keywords = sys.argv[1] if len(sys.argv) > 1 else ""

if not shutil.which("npx"):
    print("UNAVAILABLE: npx not found (Node.js required for external skill search).")
    sys.exit(0)

try:
    result = subprocess.run(
        ["npx", "--yes", "skills", "find", keywords],
        capture_output=True, text=True, timeout=15
    )
    output = result.stdout.strip()
    if result.returncode != 0 or not output:
        print(f"UNAVAILABLE: external search returned no results (exit={result.returncode}).")
        sys.exit(0)
    print(output)
except subprocess.TimeoutExpired:
    print("UNAVAILABLE: search timed out — network may be unreachable.")
    sys.exit(0)
