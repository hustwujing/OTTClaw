==============================
skill_id: install_skill
name: Install Skill
display_name: Skill Installer
enable: true
description: Installs skills from the open skills.sh ecosystem or from an uploaded skill package. Handles full-package install (SKILL.md + scripts + references + assets) with format conversion, platform adaptation, and hot-reload.
trigger: When the user asks "find a skill for X", "is there a skill that can...", "install a skill", "can you do X" where X is a specialized capability, wants to extend agent capabilities, uploads a skill package, or wants to import/migrate a skill from another platform.
==============================

## Step 0: Determine Mode

Archive uploaded / local path / import from another platform → jump to **Import from Package**. Otherwise → Step 1.

## Step 1: Search Locally

Check loaded skills in system prompt for keyword matches in name/description/trigger — no tool call. If match found, tell user and stop.

## Step 2: Search External

```
skill(action=run_script, skill_id=install_skill, script_name=search.py, args=["<keywords>"])
```

`UNAVAILABLE:` prefix → external search not possible; skip to Step 3 with empty results.

Output format: `owner/repo@skill-path`. Split on first `@`: `REPO=owner/repo` → `https://github.com/owner/repo.git`; `SKILL_DIR=skill-path`.

## Step 3: Present Results

`notify(action=options)` listing found skills (name + source) with "Install all" (if >1) and "None". If none/None: offer `notify(action=options)` with "Create from scratch" (skill_creator) or "Upload a skill package" (→ Import from Package).

## Step 4: Clone, Convert & Store

For each selected skill:

**4a.** ⚠️ Read template first: `skill(action=read_file, skill_id=skill_creator, sub_path="assets/skill_template.md")` — defines exact format.

**4b. Clone:**
```
skill(action=run_script, skill_id=install_skill, script_name=clone.py, args=["<owner>/<repo>", "<skill-path>"])
```
`ERROR:` → cloning failed, stop. `WARNING:` → path not found, use file listing to locate correct subdirectory.

**4c.** Read all files (SKILL.md + scripts/, references/, assets/).

**4d. Rewrite for OTTClaw:**

*SKILL.md*: two `==============================` separators. HEAD: `skill_id` (lowercase, hyphens→underscores, digits/letters/underscores only), `name`/`display_name` from original, `enable: true`, `description`/`trigger` derived. CONTENT: translated + trimmed (4e + 4f).

*Scripts* — rewrite each individually, never copy as-is. For any script language: replace hardcoded `/tmp/...` paths with the cross-platform isolated pattern:
```python
import os, tempfile
_TMP_ROOT = os.path.realpath(tempfile.gettempdir())
work_dir = os.path.join(_TMP_ROOT, "{}_{}".format(skill_id, os.environ.get("SKILL_SESSION_ID", "default")))
os.makedirs(work_dir, exist_ok=True)
```
For bash: replace GNU-only commands:
- `timeout N cmd` → Python `subprocess.run(..., timeout=N)`
- `date -d` → Python `datetime` or `date -j -f`
- `sed -i 's/…'` → `sed -i '' 's/…'`
- `readlink -f` → `realpath` or `os.path.realpath()`
- `stat -c` → `stat -f`
- `md5sum` → `md5 -r` or `hashlib`
- `xargs -r` → remove `-r`
- Non-trivial bash → rewrite as Python 3.
For Python: replace Linux-specific paths (`/proc`, `/etc/os-release`) and GNU subprocess calls.

*References*: adapt platform-specific API calls/paths to OTTClaw; keep unchanged otherwise.
*Assets*: keep as-is unless platform-specific.

**4e. Translate to English:** SKILL.md HEAD (`name`, `display_name`, `description`, `trigger`), SKILL.md CONTENT, script comments/messages. Do NOT translate `skill_id`, code logic, reference/asset files.

**4f. Trim SKILL.md CONTENT:** remove meta-commentary and hedging, use direct imperatives, keep ≤1 example per concept, merge single-sub-item bullets, remove content covered in reference files.

**4g. Verify then save to KV.**

Before saving, verify the assembled SKILL.md:
- Both separators are **exactly 30 `=` characters** — copy the literal string from the template read in 4a, never type from memory.
- `skill_id`, `name`, `enable`, `display_name`, `description`, `trigger` fields are present and non-empty in HEAD.

```
kv(set, _install_skill_md=<converted SKILL.md>)
kv(set, _install_scripts=<JSON array [{name, content}], [] if none>)
kv(set, _install_references=<JSON array, [] if none>)
kv(set, _install_assets=<JSON array, [] if none>)
```

## Step 5: Confirm

⚠️ **Must end with `notify(action=confirm)` — never plain text (blocks Step 6).**

List file names, then: `notify(action=confirm)` → "Confirm Install" / "Cancel". On cancel: `skill(run_script, cleanup.sh)`.

## Step 6: Write Files

**Trigger: user selected "Confirm Install".**

1. `kv(get, _install_skill_md)` → `notify(progress, "Writing SKILL.md...")`
2. Pre-write: if `skill_template.md` not read in 4a, read it now.
3. `skill(action=write, skill_id=..., content=...)` — omit `sub_path` for SKILL.md. On format error: re-read `skill(action=read_file, skill_id=skill_creator, sub_path="assets/skill_template.md")` → fix the specific issue (separator count must be exactly 30 `=`, missing HEAD fields, etc.) → `kv(set, _install_skill_md=<fixed content>)` → retry write immediately.
4. If `_install_scripts` non-empty: `notify(progress, "Writing scripts...")` → for each: `skill(write, sub_path="script/<name>")`.
5. If `_install_references` non-empty: `notify(progress, "Writing references...")` → for each: `skill(write, sub_path="references/<name>")`.
6. If `_install_assets` non-empty: `notify(progress, "Writing assets...")` → for each: `skill(write, sub_path="assets/<name>")`.
7. `notify(progress, "Reloading...")` → `skill(action=reload)`.

## Step 7: Report & Cleanup

Summary: skill_id, files written, ready to use. Note files needing manual review. `skill(run_script, cleanup.sh)`. Repeat Steps 4–7 for each remaining selected skill.

---

## Import from Package

### Step P-1: Get Package

If no path provided: `notify(action=upload, title="Upload skill package", prompt="Supported: .zip/.tar/.tar.gz/.rar/.7z. Any platform.")`.

### Step P-2: Read Format Template

⚠️ Required before rewriting. `skill(action=read_file, skill_id=skill_creator, sub_path="assets/skill_template.md")`

### Step P-3: Unzip

`skill(action=run_script, skill_id=unzip_file, script_name=unzip.py, args=[<file_path>])` → `kv(get, unzip.result)`. `ERROR:` prefix → show and stop.

### Step P-4: Read All Files

`fs(action=read, path=<output_dir>/<file>)` for every file listed.

### Step P-5: Migration Plan

Map to OTTClaw layout:

| Source | Destination | Action |
|--------|-------------|--------|
| SKILL.md / manifest / README | `SKILL.md` | Rewrite to OTTClaw format |
| `.py` / `.sh` / `.js` scripts | `script/<name>` | Rewrite (macOS-safe, Python 3 preferred) |
| Reference `.md` files | `references/<name>` | Rewrite if platform-specific; else keep |
| Images, data, config | `assets/<name>` | Keep unless platform-specific |
| Other files/subdirs | `<original-relative-path>` | Keep as-is |

`skill_id`: lowercase, digits, underscores, no hyphens. Present plan + `notify(action=confirm)` → "Proceed" / "Cancel". Cancel: stop.

### Step P-6: Rewrite Files

*SKILL.md*: HEAD per template (`skill_id`, `name`, `display_name`, `enable: true`, `description`, `trigger`). Translate to English, trim (same rules as Step 4d/4e/4f).

*Scripts*: same rewrite rules as Step 4d (bash GNU→macOS replacements, Python cross-platform fixes, `/tmp` → `tempfile.gettempdir()` + `SKILL_SESSION_ID`). Never copy as-is.

*References/Assets*: adapt platform-specific API calls/paths; otherwise keep unchanged.

*Other files*: keep content exactly; use original relative path as `sub_path`.

Verify then save to KV. Before saving, verify:
- Both separators are **exactly 30 `=` characters** — copy from template, never type from memory.
- `skill_id`, `name`, `enable`, `display_name`, `description`, `trigger` fields are present and non-empty in HEAD.

```
kv(set, _import_skill_md=<rewritten SKILL.md>)
kv(set, _import_scripts=<JSON array [{name, content}], [] if none>)
kv(set, _import_references=<JSON array, [] if none>)
kv(set, _import_assets=<JSON array, [] if none>)
kv(set, _import_others=<JSON array [{path, content}], [] if none>)
```

### Step P-7: Write

List file names, `notify(action=confirm)` → "Write files" / "Go back to edit". Then write in order:
1. SKILL.md: `skill(write, skill_id=..., content=...)` (no `sub_path`). On format error: re-read `skill(action=read_file, skill_id=skill_creator, sub_path="assets/skill_template.md")` → fix the specific issue → `kv(set, _import_skill_md=<fixed content>)` → retry immediately.
2. Each script: `skill(write, sub_path="script/<name>")`
3. Each reference: `skill(write, sub_path="references/<name>")`
4. Each asset: `skill(write, sub_path="assets/<name>")`
5. Each other: `skill(write, sub_path=<item.path>)`
6. `notify(progress, "Reloading...")` → `skill(action=reload)`

### Step P-8: Report

Summary: `skill_id`, files written, files needing manual review.

---

## Notes

- Search locally first to avoid duplicates.
- Read format template (4a) before converting.
- Save to KV before every `notify(action=confirm)`.
- Preserve all sub-files — SKILL.md without references loses context.
- Never write files without `notify(action=confirm)`.
