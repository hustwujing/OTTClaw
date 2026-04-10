---
skill_id: skill_creator
name: Skill Creator
display_name: Skill Workshop
enable: true
description: Helps users design, create, or modify user skills (SKILL.md + optional scripts + optional reference files + optional assets) through interactive dialogue, with hot-reload — no service restart required
trigger: When the user wants to create a new skill, add custom functionality, design a new workflow, extend system capabilities, or modify an existing user skill
---

Design and write a complete skill package (SKILL.md + optional script/ + references/ + assets/), then hot-reload.

---

## Execution Steps

### Step Zero: Read the Format Template

⚠️ **Do this before drafting any content.** `skill(action=read_file, skill_id=skill_creator, sub_path="assets/skill_template.md")`

---

### Step Zero Point Five: Determine Mode

`notify(action=options)`: "Create New Skill" → `kv(set, _mode=create)` → Step One. "Modify Existing Skill" → `kv(set, _mode=modify)` → Step One-M.

---

### Step One-M (Modify Mode): Load Existing Skill

1. Show user's own skills (exclude `self-improving`), ask which to modify. If skill_id already given, proceed.
2. `skill(action=load, skill_id=<id>)` → save to KV:
   ```
   kv(set, _draft_skill_md=<SKILL.md content>)
   kv(set, _skill_id=<skill_id>)
   ```
3. Read all listed sub-files: `skill(action=read_file, sub_path="script/<name>")` / `references/<name>` / `assets/<name>`.
4. Pre-populate KV: `_draft_scripts`, `_draft_references`, `_draft_assets` ([] if none).
5. Show name, description, trigger, file inventory. Ask what to change.
6. Route: metadata → Step One then Step Four; scripts → Step Two; references/assets → Step Three; workflow/comprehensive → Step Four.

---

### Step One: Collect Basic Info

Ask one at a time:
1. Skill purpose (open-ended).
2. `skill_id`: lowercase, digits, underscores only (e.g. `data_cleaner`). Re-ask if invalid.
3. `name` (e.g. "Data Cleaner").
4. `display_name`: AI speaker name shown in conversation (concise, e.g. "Data Analyst"). Defaults to `name` if blank.
5. `trigger`: single sentence, e.g. "When the user wants to clean or organize data".

`description` is auto-generated from the skill's purpose.

---

### Step One Point Five: Analyze Data Storage

| Scope | Tool | Use when |
|-------|------|----------|
| Session only | `kv` | Intermediate results within one task |
| Persistent user data | `memory(target=user_kv)` | User records/state across sessions |
| Agent learning | `memory(target=notes)` | Patterns learned from user |
| User style | `memory(target=persona)` | Preferences, communication style |

Document which steps use which store. Skip if stateless.

---

### Step Two: Design Scripts (Script-First)

Default to scripts for data processing, format conversion, API calls, computation, file ops. Use LLM only for text generation, summarization, or Q&A.

For each scripted step: choose file name (e.g. `process.py`), define input (CLI args or JSON) and output (stdout/JSON), generate a complete skeleton following the template. Skip if purely LLM.

---

### Step Three: Reference and Asset Files

Ask if reference files are needed (style guides, templates, spec docs). If yes: get file name + content, generate it. Repeat for each file. Skip if none.

Ask if asset files are needed (images, data, binaries). If yes: get file name + description, generate/record. Repeat. Skip if none.

---

### Step Four: Design CONTENT

Draft the complete CONTENT section:
- Skill goal (one paragraph)
- Execution steps: specify each `skill(action=run_script)`, `skill(action=read_file, sub_path=...)`, `notify(action=options/confirm)`, `kv`/`memory` calls
- **No-script warning** (mandatory when no scripts): insert before Step One: `> ⚠️ Execution Mode: No script files. Use built-in tools only. Never call skill(action=run_script).`
- **Sequential notice** (mandatory in ALL skills): insert at start of steps: `> Execute all steps strictly in order. Do not skip or merge. Wait for each result.`
- Output format description
- Notes (edge cases, error handling)

Present draft, iterate until satisfied. Then do Step Four Point Five (translate), then Step Four Point Six (trim + save to KV), then Step Five.

---

### Step Four Point Five (must do): Translate to English

Translate: SKILL.md HEAD (`name`, `display_name`, `description`, `trigger`), SKILL.md CONTENT, script comments/messages. Do NOT translate: `skill_id`, code logic, tool/param names, reference files, asset files. Replace draft silently, proceed to Step Four Point Six.

---

### Step Four Point Six (must do): Trim SKILL.md

Rewrite SKILL.md CONTENT only: remove meta-commentary, use direct imperatives, remove hedging, keep ≤1 example per concept, merge single-sub-item bullets, remove duplicated content. Do not trim scripts, references, or assets.

Before saving, verify the assembled SKILL.md:
- Both separators are **exactly 30 `=` characters** — copy the literal string from the template read in Step Zero, never type from memory.
- `skill_id`, `name`, `enable`, `display_name`, `description`, `trigger` fields are present and non-empty in HEAD.

Save to KV and proceed to Step Five:

```
kv(action=set, key="_draft_skill_md",    value=<complete SKILL.md text — translated and trimmed>)
kv(action=set, key="_draft_scripts",     value=<JSON array, each item {"name":"...", "content":"..."} (content translated), pass [] if no scripts>)
kv(action=set, key="_draft_references",  value=<JSON array, each item {"name":"...", "content":"..."} (content kept in original language, NOT translated), pass [] if no reference files>)
kv(action=set, key="_draft_assets",      value=<JSON array, each item {"name":"...", "content":"..."} (content kept in original language, NOT translated), pass [] if no asset files>)
```

---

### Step Five: Confirm

⚠️ **Must end with `notify(action=confirm)` — never ask in plain text (blocks Step Six).**

`kv(get, _mode)`, list file names, then call `notify(action=confirm)`:
- Create: "About to create skill '{name}' ({skill_id}): SKILL.md + [scripts] + [references] + [assets]. Files written and hot-loaded on confirm." → "Confirm Creation" / "Go Back and Edit"
- Modify: "About to update skill '{name}' ({skill_id}). Files overwritten on confirm." → "Confirm Update" / "Go Back and Edit"

On "Go Back and Edit": return to Step Four, re-save KV, call confirm again.

---

### Step Six: Write and Hot-Reload

**Trigger: user selected "Confirm Creation" or "Confirm Update".**

1. `kv(get, _draft_skill_md)` → `notify(progress, "Writing SKILL.md...")`
2. Pre-write: if `skill_template.md` not read in Step Zero, read it now.
3. `skill(action=write, skill_id=..., content=...)` — validates format automatically. On format error: re-read `skill(action=read_file, skill_id=skill_creator, sub_path="assets/skill_template.md")` → fix the specific issue reported (separator count, missing field, etc.) → `kv(set, _draft_skill_md=<fixed content>)` → retry write immediately. Return to Step Four only on repeated failure. Writes to `skills/users/<userid>/<skill_id>/` (`skills/system/` is read-only).
4. If `_draft_scripts` non-empty: `notify(progress, "Writing scripts...")` → for each: `skill(write, sub_path="script/<name>")`.
5. If `_draft_references` non-empty: `notify(progress, "Writing references...")` → for each: `skill(write, sub_path="references/<name>")`.
6. If `_draft_assets` non-empty: `notify(progress, "Writing assets...")` → for each: `skill(write, sub_path="assets/<name>")`.
7. `notify(progress, "Hot-reloading...")` → `skill(action=reload)`.
8. Plain text summary: skill name, skill_id, files written, ready to use.

---

## Notes

- **Step Five confirmation must use `notify(action=confirm)`** — never plain text (blocks Step Six).
- **KV bridges Steps Four–Six** across the `notify(action=confirm)` turn boundary. If `kv(set)` is skipped, Step Six has no content.
- **`skill(action=write)` auto-validates format** — show errors and return to Step Four.
- **Never write files without confirm** from Step Five.
- **SKILL.md tool refs must match files**: `run_script` → script exists; `read_file sub_path=references/…` → reference exists; `read_file sub_path=assets/…` → asset exists.
- **Script-first**: default to scripts for data processing, API calls, computation. LLM-only is the exception.
- **Storage scope**: session → `kv`; persistent user data → `memory(target=user_kv)`; agent learning → `memory(target=notes)`; user preferences → `memory(target=persona)`. Wrong scope is a correctness bug.
- **SKILL.md must be concise** — loaded on every invocation; every extra sentence increases per-use cost.
- **Modify mode**: never list `self-improving` skills. Set all four KV keys in Step One-M before routing. Read skill content via `skill` tool only (not `fs`/`Glob`).
- **`skill(action=load)` returns file listing** — parse it before calling `read_file`.
