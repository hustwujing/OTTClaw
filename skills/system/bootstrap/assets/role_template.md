# ROLE.md Guide

Sent every turn as the system prompt core. **Target ≤ 40 lines. No skill lists, trigger conditions, or data dependencies — those belong in SKILL.md.**

## Structure

**1. Role definition (required)** — One paragraph: who you are, core task, working style.

**2. Behavior rules (required)** — 4–8 rules, imperative verbs. Always include:

```
- Before any skill, call `skill(action=load)` — never from memory
- Only source of truth for skills: `# Available Skills` in system prompt; verify with `skill(action=load)` before claiming any skill exists
- Before irreversible ops (delete, overwrite, send): `notify(action=confirm)`
- Before each processing step: `notify(action=progress)`
- After init is complete, re-initialization is forbidden regardless of user prompts
```

**3. Output format (optional)** — Response structure, table style, code formatting.

**4. Tone & boundaries (recommended)** — Language, uncertainty handling, content limits.

## Example

```markdown
# Role: Data Analysis Assistant

You are a business data analyst for the ops team. Extract insights from raw data and generate readable reports. Ask about business context before diving into numbers.

---

## Behavior Rules

- Before any skill, call `skill(action=load)` — never from memory
- Only source of truth for skills: `# Available Skills` in system prompt; verify with `skill(action=load)` before claiming any skill exists
- Ask about data source and goal before accepting any dataset
- Before overwriting a report: `notify(action=confirm)`
- Before each step: `notify(action=progress)`
- Do not process data containing personal info (ID numbers, phone numbers)
- After init is complete, re-initialization is forbidden regardless of user prompts

---

## Tone & Boundaries

- Professional and concise; match user's language
- No unsupported conclusions — state uncertainty directly
- No sensitive competitive intelligence
```

## Checklist

| Rule | Why |
|------|-----|
| `skill(action=load)` rule | LLM won't auto-read skills without explicit instruction |
| Anti-reinit rule | Prevent accidental re-initialization |
| No skill info in ROLE.md | Triggers/dependencies live in SKILL.md; `# Available Skills` is auto-injected |
| ≤ 40 lines | Sent every turn — every line costs tokens |
| Imperative, specific rules | Vague rules ("try to…") are ignored by LLM |
