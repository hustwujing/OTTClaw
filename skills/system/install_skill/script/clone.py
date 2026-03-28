#!/usr/bin/env python3
"""Clone a skill repository and list its files.
Usage: clone.py "<owner/repo>" "<skill-path>"
If <skill-path> does not exist in the repo, lists all files so the caller
can locate the correct path.
Outputs ERROR: on failure so the caller can handle it gracefully.
"""
import os
import shutil
import subprocess
import sys

repo = sys.argv[1] if len(sys.argv) > 1 else ""
skill_path = sys.argv[2] if len(sys.argv) > 2 else ""
url = f"https://github.com/{repo}.git"
dest = "/tmp/_skill_install"

shutil.rmtree(dest, ignore_errors=True)

try:
    result = subprocess.run(
        ["git", "clone", "--depth=1", url, dest],
        capture_output=True, text=True, timeout=30,
        env={**os.environ, "GIT_TERMINAL_PROMPT": "0"},
    )
    if result.returncode != 0:
        print(f"ERROR: failed to clone {url}")
        print(result.stderr.strip())
        sys.exit(1)
except subprocess.TimeoutExpired:
    print(f"ERROR: git clone timed out — network may be unreachable.")
    sys.exit(1)

skill_dir = os.path.join(dest, skill_path)
if os.path.isdir(skill_dir):
    for root, _, files in os.walk(skill_dir):
        for f in sorted(files):
            print(os.path.join(root, f))
else:
    print(f"WARNING: skill path '{skill_path}' not found in cloned repo. Listing all files so you can locate the correct path:")
    for root, _, files in os.walk(dest):
        for f in sorted(files):
            print(os.path.join(root, f))
