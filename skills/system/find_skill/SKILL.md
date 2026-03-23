==============================
skill_id: find_skill
name: Find & Install Skills
display_name: Skill Finder
enable: true
description: Discovers skills from local library and the open skills.sh ecosystem. Handles full-package install (SKILL.md + scripts + references + assets) with format conversion and hot-reload.
trigger: When the user asks "find a skill for X", "is there a skill that can...", "install a skill", "can you do X" where X is a specialized capability, or wants to extend agent capabilities.
requires_bins: npx
==============================

# Find & Install Skills

## Step 1: Search locally first

Call `skill(action=list)` and scan results for keyword matches in name/description/trigger. If found, tell user it exists and stop. Otherwise proceed.

## Step 2: Search external ecosystem

```bash
npx skills find "<keywords>"
```

If `npx` unavailable, inform user Node.js is needed.

## Step 3: Present results & let user choose

Use `notify(action=options)` listing each found skill with name + source. Include "Install all" (if >1) and "None" options.

If none found or user picks "None": offer direct help or suggest `skill_creator`.

## Step 4: Explore package structure

For each selected skill, clone and list all files:

```bash
cd /tmp && rm -rf _skill_install
git clone --depth=1 "https://github.com/<owner>/<repo>.git" _skill_install 2>/dev/null
find /tmp/_skill_install/<skill-path>/ -type f | sort
```

This reveals: SKILL.md, script/*, references/*, assets/*.

## Step 5: Confirm with user

Use `notify(action=confirm)` showing file list and total count. Label: "Confirm Install" / "Cancel".

## Step 6: Convert & write

**Header conversion** — YAML frontmatter → OTTClaw format:
- `skill_id`: from name, lowercase, hyphens→underscores
- `name`, `display_name`: from original name
- `enable`: always true
- `description`, `trigger`: derived from original description

**Write all files** with progress notifications:
1. `skill(action=write, skill_id=..., content=<converted SKILL.md>)`
2. Each sub-file: `skill(action=write, skill_id=..., content=..., sub_path="script/<name>")` / `references/<name>` / `assets/<name>`

## Step 7: Reload & report

```
skill(action=reload)
```

Output summary: skill_id, files written, ready to use.

## Step 8: Cleanup

```bash
rm -rf /tmp/_skill_install
```

Repeat Steps 4-8 for each selected skill.

## Notes

- Always search locally first to avoid duplicates
- Never skip user confirmation before writing
- Preserve all sub-files — SKILL.md without its references may lose critical context
