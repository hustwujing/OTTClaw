# Unzip Assistant

A general-purpose extraction tool that supports multiple archive formats and returns a file list after extraction for subsequent processing.

==============================
skill_id: unzip_file
name: Unzip Assistant
display_name: Unzip Assistant
description: Accepts an archive file (zip/tar/tar.gz/rar/7z), extracts it to the output directory, returns the list of internal file paths, and stores it in KV
trigger: Triggered when the user uploads an archive and says "help me unzip", "extract this file", "unzip this", "unpack this archive", etc.
enable: true
==============================

## Execution Flow

### 1. Identify the Archive File

Extract the uploaded archive path from the user's message (format: `[File: uploads/xxx/filename.zip]`).

Supported formats:
- `.zip`
- `.tar`
- `.tar.gz` / `.tgz`
- `.rar`
- `.7z`

If the user has not uploaded a file or the file format is not supported, politely inform the user to upload a supported archive format.

### 2. Run the Extraction Script

Call `skill(action=run_script)` to execute `script/unzip.py`:

```
skill(action=run_script, skill_id="unzip_file", script_name="unzip.py", args=[archive_path])
```

The script extracts to `output/{bucket}/{name}_unzipped/` and outputs JSON: `{"output_dir": "...", "files": [...]}`.

### 3. Store the Result in KV

Store result in KV:

```
kv(action=set, key="unzip.result", value={
  "source": "original archive path",
  "output_dir": "extraction directory path",
  "files": ["file1 path", "file2 path", ...]
})
```

### 4. Return the Result to the User

Display: success message, extraction directory, file list (max 20, show total count).

Example output:
```
Extraction complete!

Extraction directory: output/A/myfiles_unzipped/
15 files in total:
  - document.pdf
  - images/photo1.jpg
  - images/photo2.jpg
  - ...

The file list has been stored in KV (key: unzip.result) and is available for subsequent skills.
```

## Error Handling

- If the archive is corrupted or the format is not supported, inform the user of the specific error
- If an extraction tool is not installed (e.g., unrar, 7z), advise the user to install the corresponding tool
