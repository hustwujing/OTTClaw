---
skill_id: unzip_file
name: Unzip Assistant
display_name: Unzip Assistant
description: Accepts an archive file (zip/tar/tar.gz/rar/7z), extracts it to the output directory, returns the list of internal file paths, and stores it in KV
trigger: Triggered when the user uploads an archive and says "help me unzip", "extract this file", "unzip this", "unpack this archive", etc.
enable: true
---

## Steps

### 1. Identify Archive

Extract path from user message (`[File: uploads/xxx/filename.zip]`). Supported: `.zip` `.tar` `.tar.gz` `.tgz` `.rar` `.7z`. If unsupported format, ask for a supported archive.

### 2. Run Extraction

```
skill(action=run_script, skill_id="unzip_file", script_name="unzip.py", args=[archive_path])
```

Extracts to `output/{bucket}/{name}_unzipped/`. Returns JSON: `{"output_dir": "...", "files": [...]}`.

### 3. Store in KV

```
kv(action=set, key="unzip.result", value={"source": ..., "output_dir": ..., "files": [...]})
```

### 4. Report

Show extraction directory and file list (max 20, with total count). Note KV key for downstream skills.

## Errors

Show specific error for corrupted/unsupported archives. If tool missing (unrar, 7z), advise user to install it.
