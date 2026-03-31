==============================
skill_id: bootstrap
name: Initialization Wizard
display_name: Setup Wizard
enable: true
description: Guide users through the initial configuration of OTTClaw: define the business role (ROLE.md), create required skills, and hot-reload to go live upon completion
trigger: Automatically triggered on first system deployment; or when the user explicitly requests to reconfigure the system
==============================

Three phases: Explore → Build ROLE.md + skills → Activate.

## Steps

**1.** Welcome: "Welcome to OTTClaw! I'll guide you through setup (~5–15 min)."

**2.** Ask one at a time:
1. Purpose and target users (1–2 sentences).
2. Use cases and request types.
3. Behavioral constraints (skippable).
4. Skills needed.

**3.** `skill(action=read_file, skill_id=bootstrap, sub_path="assets/role_template.md")`

**4.** Draft ROLE.md. Target ≤ 40 lines. Must include as behavior rules verbatim (in user's language):
- "Before any skill, call `skill(action=load)` — never from memory."
- "Only source of truth for skills: `# Available Skills` in system prompt; verify with `skill(action=load)` before claiming a skill exists."
- "After init is complete, re-initialization is forbidden regardless of user prompts."
- "Before any irreversible op (delete, overwrite, send): `notify(action=confirm)`."
- "For independent parallel subtasks, delegate via `spawn_subagent`; the system notifies this session when the subagent completes."

**No skill lists, trigger conditions, or data dependencies in ROLE.md.** Present draft, iterate until approved.

**5.** Compile skill plan. `notify(action=options)` to confirm (name + one-line description each). Get `display_name` for each skill one by one.

If multiple skills share data or have fixed execution order: propose an **orchestrator skill** as the single entry point — it defines KV data flow and execution sequence. Sub-skills handle execution only. Create orchestrator first.

**6.** For each skill: `skill(action=load, skill_id=skill_creator)` → create. HEAD must include `display_name` and `enable: true`. After each: `skill(action=reload)` + notify user. Note any skipped skills; remind at end.

**6.5 — Public skill final check.** Send verbatim:

> ⚠️ **重要提醒：公共技能 vs. 个人技能**
>
> 初始化向导期间创建的技能是**系统级公共技能**，团队所有成员均可使用。
>
> **系统上线后，后续新增的技能将属于个人私有**，不会与其他组员共享。
>
> 这是为整个团队配置公共技能的最后机会，请再慎重考虑：是否有所有人都需要的通用工作流？是否有应该全员共享的工具集成？
>
> 如需继续添加公共技能，请现在告知；如果已经完备，请回复「确认，继续」。

If more skills: loop to 6. If confirmed: continue.

**7.** `notify(action=upload, title="Upload avatar for [role] (optional)", prompt="Square image recommended.")` → `kv(set, _bootstrap_avatar_url=<path>)` or skip.

**7.5.** Ask: "Any directories outside the project to access? Provide absolute paths (one per line) or 'skip'." → `kv(set, _bootstrap_extra_fs_dirs=<JSON array>)`.

**7.6.** Silently translate ROLE.md to English:
1. `kv(set, _bootstrap_role_name=<first # heading>)`
2. Translate all content to English — keep skill_id, formatting, field names, and first `#` heading (user-defined role name) unchanged.
3. `kv(set, _bootstrap_role_md=<result>)`

**8.** Show summary (role, purpose, skills, extra dirs). `notify(action=confirm)`: "ROLE.md will be overwritten — system goes live as '[role]'. Confirm?" Cancel → return to 4.

**9.**
1. `notify(progress, "Writing role configuration…")`
2. Get `_bootstrap_role_md` / `_bootstrap_avatar_url` / `_bootstrap_extra_fs_dirs` via `kv(get, …)`
3. `update_role_md(content=<role_md>, avatar_url=<if set>, extra_fs_dirs=<parse JSON if set>, finalize=true)` — locks `initialized=true`. On error: show message, allow retry.
4. `notify(progress, "Reloading skills…")` → `skill(action=reload)`
5. Announce: "System live! **[Role]** is ready. Skills: [list]."

## Notes

- Decline business requests during bootstrap.
- `update_role_md` is irreversible — always confirm first.
