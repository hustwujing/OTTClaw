==============================
skill_id: wecom_setup
name: WeCom Bot Configuration Wizard
display_name: WeCom Setup Assistant
enable: true
description: Guides users through configuring a WeCom group bot (Webhook mode), with support for test verification
trigger: Triggered when the user says "configure WeCom", "set up WeCom", "WeCom configuration", "connect WeCom", or similar requests
==============================

## Skill Goal

Help the user configure a WeCom group bot Webhook (push-only; cannot receive messages).

---

## Execution Steps

### Step 1: Check Current Configuration Status

Call `wecom(action=get_config)` to see if a configuration already exists.

- If **already configured**: Inform the user of the current Webhook URL (masked), and ask whether they want to **reconfigure**
- If **not configured**: Proceed directly to Step 2

---

### Step 2: Collect Webhook URL

Inform the user:
> In WeCom group → "..." top-right → "Add Group Bot" → "Create a new bot" → copy the **Webhook URL** (`https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...`).

Wait for the user to paste the Webhook URL.

Validate the format: the URL should start with `https://qyapi.weixin.qq.com/cgi-bin/webhook/send`; if it does not match, prompt the user to obtain it again.

---

### Step 3: Confirm and Save

Call `notify(action=confirm)`:
```
message: "The following WeCom configuration is about to be saved:\nWebhook URL: {first 20 characters}****"
confirm_label: "Confirm Save"
cancel_label: "Cancel"
```

After the user confirms, call `wecom(action=set_config, ...)`:
```json
{
  "action": "set_config",
  "webhook_url": "<user-entered webhook_url>"
}
```

---

### Step 4: Test Verification

After saving, ask the user whether they want to send a test message.

If the user agrees, call `wecom(action=send, ...)`:
```json
{
  "action": "send",
  "text": "🎉 WeCom bot configured successfully! I can now send messages to this group.",
  "msgtype": "markdown"
}
```

- If the send succeeds: proceed to Step 5
- If the send fails: display the error message and prompt the user to check:
  1. Whether the Webhook URL was copied in full (including the `key=` part)
  2. Whether the bot was successfully added to the group chat
  3. Whether the group chat has been disbanded or the bot has been removed

---

### Step 5: Completion

After successful configuration, summarize for the user:

> ✅ WeCom bot configured! The AI can now proactively send messages to your WeCom group. Use `msgtype: "markdown"` for formatted content.
