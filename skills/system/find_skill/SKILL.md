==============================
skill_id: find_skill
name: Find & Install Skills
display_name: Skill Finder
enable: true
description: Discovers skills from local library and the open skills.sh ecosystem. Handles full-package install (SKILL.md + scripts + references + assets) with format conversion and hot-reload.
trigger: When the user asks "find a skill for X", "is there a skill that can...", "install a skill", "can you do X" where X is a specialized capability, or wants to extend agent capabilities.
==============================

# Find & Install Skills

## Step 1: Search locally first

All loaded skill descriptions are already present in the current context (system prompt). Check them for keyword matches in name/description/trigger — no tool call needed. If a match exists, tell the user and stop. Otherwise proceed.

## Step 2: Search external ecosystem

```bash
npx skills find "<keywords>"
```

If `npx` is unavailable, inform the user that Node.js is required and stop.

**Parse the output format.** Each result line looks like:

```
owner/repo@skill-path
```

Split on the first `@` to get:
- `REPO` = `owner/repo` → GitHub URL: `https://github.com/owner/repo.git`
- `SKILL_DIR` = `skill-path` → subdirectory inside the cloned repo

Example: `supercent-io/skills-template@technical-writing`
- REPO URL: `https://github.com/supercent-io/skills-template.git`
- SKILL_DIR: `technical-writing`

## Step 3: Present results & let user choose

Use `notify(action=options)` listing each found skill with name + source. Include "Install all" (if >1 result) and "None" options.

If none found or user picks "None": offer direct help or suggest `skill_creator`.

## Step 4: Clone, convert & store in KV

For each selected skill:

**4a. Read the OTTClaw format template first:**

⚠️ **Required before steps 4b–4g. Do not convert or write any SKILL.md without completing this step.**

```
skill(action=read_asset, skill_id=skill_creator, asset_name=skill_template.md)
```

This is required before any conversion. The template defines the exact format (30 `=` separators, HEAD fields, CONTENT structure) that the converted SKILL.md must follow.

**4b. Clone the repository:**

```bash
cd /tmp && rm -rf _skill_install
git clone --depth=1 "https://github.com/<owner>/<repo>.git" _skill_install 2>/dev/null
find /tmp/_skill_install/<skill-path>/ -type f | sort
```

**4c. Read all file contents** (SKILL.md + every file under scripts/, references/, assets/).

**4d. Convert SKILL.md to OTTClaw format** following the template loaded in 4a:
- Build the full file with two `==============================` separators (exactly 30 `=` each)
- HEAD fields:
  - `skill_id`: derived from name, lowercase, **hyphens → underscores** (e.g. `c4-architecture` → `c4_architecture`). Only lowercase letters, digits, and underscores are allowed — never hyphens.
  - `name`, `display_name`: from original name
  - `enable`: always `true`
  - `description`, `trigger`: derived from original description
- CONTENT: the skill body, translated and trimmed (steps 4e and 4f below)

**4e. Translate all content to English:**
- SKILL.md HEAD: translate `name`, `display_name`, `description`, `trigger` (`skill_id` unchanged)
- SKILL.md CONTENT: translate all body text
- Script files: translate comments and string messages only (do not alter code logic)
- Reference and asset files: keep in original language — do NOT translate

**4f. Trim SKILL.md CONTENT for token efficiency:**
- Remove meta-commentary and hedging ("if possible", "note that", "keep in mind that")
- Replace "You should do X in order to Y" with "Do X."
- Keep at most one minimal example per concept
- Merge single-sub-item bullet points into one line
- Remove content already covered in reference files

**4g. Save everything to KV** before calling confirm:

```
kv(action=set, key="_install_skill_md",   value=<converted+translated+trimmed SKILL.md full text>)
kv(action=set, key="_install_scripts",    value=<JSON array [{"name":"...", "content":"..."}], [] if none>)
kv(action=set, key="_install_references", value=<JSON array [{"name":"...", "content":"..."}], [] if none>)
kv(action=set, key="_install_assets",     value=<JSON array [{"name":"...", "content":"..."}], [] if none>)
```

## Step 5: Confirm with user

⚠️ **This step must end with a `notify(action=confirm)` tool call — never ask for confirmation in plain text, as that will prevent Step 6 from executing.**

List the files about to be written (names only, no content), then **immediately call `notify(action=confirm)`**:

- Confirm label: "Confirm Install"
- Cancel label: "Cancel"

If the user cancels, stop and clean up: `rm -rf /tmp/_skill_install`.

## Step 6: Write files

**Trigger: user selected "Confirm Install" in the `notify(action=confirm)` from Step 5.**

Retrieve from KV and write in order:

1. `kv(action=get, key="_install_skill_md")`
2. `notify(action=progress)`: "Writing SKILL.md..."
2.5. **Pre-write check**: if `skill_template.md` was not read in Step 4a, call `skill(action=read_asset, skill_id=skill_creator, asset_name=skill_template.md)` now before writing.
3. `skill(action=write, skill_id=..., content=<retrieved content>)` — **do NOT include `sub_path`; omitting it is how SKILL.md is written**
   - If an error is returned, show it to the user and stop
4. `kv(action=get, key="_install_scripts")` — skip if empty array
5. (If scripts exist) `notify(action=progress)`: "Writing scripts..."
6. (If scripts exist) For each item: `skill(action=write, skill_id=..., content=..., sub_path="script/<name>")`
7. `kv(action=get, key="_install_references")` — skip if empty array
8. (If references exist) `notify(action=progress)`: "Writing references..."
9. (If references exist) For each item: `skill(action=write, skill_id=..., content=..., sub_path="references/<name>")`
10. `kv(action=get, key="_install_assets")` — skip if empty array
11. (If assets exist) `notify(action=progress)`: "Writing assets..."
12. (If assets exist) For each item: `skill(action=write, skill_id=..., content=..., sub_path="assets/<name>")`
13. `notify(action=progress)`: "Reloading skill list..."
14. `skill(action=reload)`

## Step 7: Report & cleanup

Output summary: skill_id, list of files written, ready to use.

```bash
rm -rf /tmp/_skill_install
```

Repeat Steps 4–7 for each remaining selected skill.

## Notes

- Always search locally first to avoid duplicates.
- Read the format template (4a) before converting — never guess the OTTClaw format from memory.
- KV is the only reliable bridge across the `notify(action=confirm)` boundary — always save content before calling confirm.
- Preserve all sub-files — a SKILL.md without its references may lose critical context.
- Never write files without user confirmation via `notify(action=confirm)`.
