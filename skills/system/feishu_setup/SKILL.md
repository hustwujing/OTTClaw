==============================
skill_id: feishu_setup
name: Feishu Bot Configuration Wizard
display_name: Feishu Setup Assistant
enable: true
description: Guides users through interactive configuration of a Feishu bot (Bot API long-connection mode or Webhook mode), with support for test verification
trigger: Triggered when the user says "configure Feishu", "set up Feishu bot", "Feishu configuration", "connect Feishu", or similar requests
==============================

## Modes

- **Bot API**: receives + sends messages (App ID + App Secret, long-connection)
- **Webhook**: push-only to a group (Webhook URL only)

---

## Steps

### Step 1: Check Config

Call `feishu(action=get_config)`. If already configured, show summary and ask to reconfigure or update. Otherwise proceed.

---

### Step 2: Select Mode

Call `notify(action=options)`:
- "Bot API Mode (receive + send)" → value: "bot"
- "Webhook Mode (push to group only)" → value: "webhook"
- "Explain the difference" → value: "help" (explain, then show options again)

---

### Step 3A: Bot API Mode

1. Ask for App ID: Feishu Developer Console (https://open.feishu.cn) → app → Credentials & Basic Info (format: `cli_xxxxxxxxxx`).
2. Ask for App Secret: same page, stored encrypted.
3. Ask for Self Open ID (optional): bind `open_id` (starts with `ou_`) or `user_id` for proactive Web→Feishu sends. To get: https://open.feishu.cn/api-explorer/ → Messages → Send Message → set `receive_id_type=open_id` → copy open_id. Reply "skip" to skip.
4. `notify(action=confirm)` showing App ID + masked secret + open_id.
5. `feishu(action=set_config, app_id=..., app_secret=..., self_open_id=...)` → proceed to Step 4.

---

### Step 3B: Webhook Mode

1. Ask for Webhook URL: Feishu group → Settings → Group Bots → Add Bot → Custom Bot → copy URL (starts with `https://open.feishu.cn/open-apis/bot/v2/hook/`).
2. `notify(action=confirm)` showing masked URL.
3. `feishu(action=set_config, webhook_url=...)` → proceed to Step 4.

---

### Step 4: Test

Ask if the user wants a test message.

**Bot mode**: ask for open_id, then `feishu(action=send, receive_id=<open_id>, receive_id_type="open_id", text="Feishu bot configured!")`. On failure, check: App ID/Secret correct, app published, permissions granted (Get User Info + Send Messages).

**Webhook mode**: `feishu(action=webhook, webhook_url=<saved>, text="Feishu Webhook configured!")`.

---

### Step 5: Done

**Bot mode**: "Feishu bot ready. Send direct messages or @mention in groups. Web and Feishu histories are independent."

**Webhook mode**: "Feishu Webhook ready. To also receive messages, upgrade to Bot API mode."

---

## Notes

- App Secret encrypted with AES-GCM; key from env `FEISHU_ENCRYPT_KEY`. If missing, `set_config` returns an error — ask admin to set it.
- Long-connection requires no HTTP callback; bot connects proactively.
- Each user configures their own bot independently.
