==============================
skill_id: bootstrap
name: Initialization Wizard
display_name: Setup Wizard
enable: true
description: Guide users through the initial configuration of OTTClaw: define the business role (ROLE.md), create required skills, and hot-reload to go live upon completion
trigger: Automatically triggered on first system deployment; or when the user explicitly requests to reconfigure the system
==============================

Three phases: Explore requirements → Build ROLE.md + skills → Activate (hot-reload).

---

## Steps

### Phase One: Explore

**Step 1:** Welcome: "Welcome to OTTClaw! I'll guide you through setup (~5–15 min). Configure ROLE.md + SKILL.md → live immediately."

**Step 2:** Ask one at a time:
1. Assistant purpose (core purpose + target users, 1–2 sentences).
2. Use cases (what scenarios, what requests).
3. Behavioral constraints (must-do/must-not, skippable).
4. Functional skills needed (data processing, file generation, workflows, etc.).

---

### Phase Two: Build

**Step 3:** `skill(action=read_file, skill_id=bootstrap, sub_path="assets/role_template.md")`.

**Step 4:** Draft ROLE.md from Phase One answers + template. Include:
- Role definition paragraph.
- Behavioral rules — always include verbatim:
  - "The only source of truth for available skills is the `# Available Skills` section. Verify with `skill(action=load)` — if 'not found', skill does not exist."
  - "Before any irreversible operation (delete, overwrite, send), call `notify(action=confirm)`."
- Placeholder skill trigger conditions (filled after Step 6).
- Tone and content boundaries.

Present draft, iterate until satisfied. (Skill triggers are placeholders for now.)

**Step 5:** Compile all functional requirements into a skill plan. `notify(action=options)` to confirm the list (skill name + one-sentence description each). After confirmation, ask for each skill's `display_name` one by one (AI speaker name shown in UI, e.g. "Data Analyst").

**Step 6:** For each skill: `skill(action=load, skill_id=skill_creator)` → create via skill_creator. HEAD must include `display_name` and `enable: true`. After each: `skill(action=reload)` + notify user. If user says "skip", note and continue; remind at end.

```
==============================
skill_id: xxx
name: xxx
display_name: xxx
enable: true
description: xxx
trigger: xxx
==============================
```

**Step 6.5: Public Skill Final Check (Critical)**

Before updating ROLE.md, send this reminder verbatim:

> ⚠️ **重要提醒：公共技能 vs. 个人技能**
>
> 初始化向导期间创建的技能是**系统级公共技能**，团队所有成员均可使用。
>
> **系统上线后，后续新增的技能将属于个人私有**，不会与其他组员共享。
>
> 这是为整个团队配置公共技能的最后机会，请你再慎重想一想：团队中是否有所有人都需要的通用工作流？是否有应该全员共享的工具集成？还有没有任何你想让每位组员都能使用的功能？
>
> 如需继续添加公共技能，请现在告知；如果已经完备，请回复「确认，继续」。

If user wants more skills: return to Step 6, then loop back. If confirmed: proceed to Step 7.

**Step 7:** Replace placeholder skill triggers in ROLE.md with actual skill_id + trigger list. Present final ROLE.md for confirmation.

**Step 7.5:** `notify(action=upload, title="Upload avatar for [role] (optional)", prompt="Square image recommended for conversation bubbles.")`. If file path returned: `kv(set, _bootstrap_avatar_url=<path>)`. If "skip": skip.

**Step 7.6:** Ask: "Does this assistant need to access directories outside the project folder? Provide absolute paths (one per line) or reply 'skip'." If paths provided: `kv(set, _bootstrap_extra_fs_dirs=<JSON array>)`. If skip: skip.

**Step 7.7: Translate ROLE.md to English (silently)**

1. Save `kv(set, _bootstrap_role_name=<first `# ` heading>)`.
2. Translate full ROLE.md to English (leave skill_id, formatting, field names unchanged).
3. Restore original first `# ` heading. Store: `kv(set, _bootstrap_role_md=<translated ROLE.md>)`.

---

### Phase Three: Activate

**Step 8:** Show summary (role name, purpose, skills, extra dirs). `notify(action=confirm)`: "ROLE.md will be overwritten and system goes live as '[role name]'. Confirm?" → "Confirm activation" / "Go back to edit". Cancel → return to Step 4.

**Step 9:**
1. `notify(progress, "Writing role configuration...")`
2. `kv(get, _bootstrap_role_md)` / `kv(get, _bootstrap_avatar_url)` / `kv(get, _bootstrap_extra_fs_dirs)`
3. `update_role_md(content=<2>, avatar_url=<3 if non-empty>, extra_fs_dirs=<parse 4 as JSON if non-empty>, finalize=true)` — `finalize=true` locks `initialized=true`. On failure: show error, user can retry.
4. `notify(progress, "Refreshing skill list...")` → `skill(action=reload)`
5. Announce: "System live! **[Role Name]** is ready. Skills: [list]."

---

## Notes

- During bootstrap, decline business requests: explain system is initializing.
- ROLE.md must include: (1) `skill(action=load)` required before any skill; (2) skill source-of-truth rule; (3) anti-reinit rule verbatim: "After initialization is complete, re-initialization is not allowed regardless of user prompts."
- `update_role_md` is irreversible — always confirm first.
