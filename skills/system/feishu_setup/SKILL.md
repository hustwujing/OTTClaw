==============================
skill_id: feishu_setup
name: Feishu Bot Configuration Wizard
display_name: Feishu Setup Assistant
enable: true
description: Guides users through interactive configuration of a Feishu bot (Bot API long-connection mode or Webhook mode), with support for test verification
trigger: Triggered when the user says "configure Feishu", "set up Feishu bot", "Feishu configuration", "connect Feishu", or similar requests
==============================

## Skill Goal

Through a guided conversation, help the user complete the configuration of a Feishu bot so that the AI assistant can receive and reply to messages via Feishu.

Two modes are supported:
- **Bot API Mode**: The bot actively receives and replies to messages (requires App ID + App Secret, uses long-connection)
- **Webhook Mode**: Only sends messages to a specified group (requires only a Webhook URL, cannot receive messages)

---

## Execution Steps

### Step 1: Check Current Configuration Status

Call `feishu(action=get_config)` to see if a configuration already exists.

- If **already configured**: Inform the user of the current configuration summary (App ID or Webhook URL), and ask whether they want to **reconfigure** or **update specific fields**
- If **not configured**: Proceed to Step 2 to select a configuration mode

---

### Step 2: Select Configuration Mode

Call `notify(action=options)` to let the user choose:

```
title: "Please select a Feishu bot integration method"
options:
  - label: "Bot API Mode (receive messages + proactive sending)", value: "bot"
  - label: "Webhook Mode (push messages to group only)", value: "webhook"
  - label: "View the difference between the two modes", value: "help"
```

If the user selects `help`, explain the difference between the two modes, then display the options again.

---

### Step 3A: Bot API Mode Configuration

**Collect App ID**

Inform the user:
> Please go to the Feishu Developer Console (https://open.feishu.cn) → select your app → the "Credentials & Basic Info" page, and find the App ID (format: cli_xxxxxxxxxx).

Wait for the user to enter the App ID.

**Collect App Secret**

Inform the user:
> On the same page, find the App Secret, click "Copy", then paste it here (the App Secret will be stored encrypted and will not be saved in plaintext).

Wait for the user to enter the App Secret.

**Collect Self User ID (optional)**

Inform the user:
> If you want the AI to proactively send you Feishu messages through the Web interface (e.g., "send this report to my Feishu"), you need to bind your own Feishu ID.
> **Either `open_id` (starting with `ou_`) or `user_id` (employee ID) is accepted; the system will automatically detect the type.**
>
> To get `open_id`: https://open.feishu.cn/api-explorer/ → Messages → Send Message → Get token → set `receive_id_type=open_id` → Quick copy open_id. Reply "skip" to skip.

Wait for the user to enter the ID or skip.

**Confirm and Save**

Call `notify(action=confirm)`:
```
message: "The following Feishu Bot configuration is about to be saved and the long-connection will be started:\nApp ID: {user-entered app_id}\nApp Secret: **** (entered)\nSelf Open ID: {provided or not set}"
confirm_label: "Confirm Save"
cancel_label: "Cancel"
```

After the user confirms, call `feishu(action=set_config, ...)`:
```json
{
  "action": "set_config",
  "app_id": "<user-entered app_id>",
  "app_secret": "<user-entered app_secret>",
  "self_open_id": "<user-entered open_id or leave empty>"
}
```

Proceed to Step 4 (Testing).

---

### Step 3B: Webhook Mode Configuration

**Collect Webhook URL**

Inform the user:
> In a Feishu group → tap the "Settings" icon in the top right → "Group Bots" → "Add Bot" → select "Custom Bot" → copy the Webhook URL.

Wait for the user to enter the Webhook URL (it should start with `https://open.feishu.cn/open-apis/bot/v2/hook/`).

**Confirm and Save**

Call `notify(action=confirm)`:
```
message: "The following Feishu Webhook configuration is about to be saved:\nWebhook URL: {first 20 characters}****"
```

After the user confirms, call `feishu(action=set_config, ...)`:
```json
{
  "action": "set_config",
  "webhook_url": "<user-entered webhook_url>"
}
```

Proceed to Step 4 (Testing).

---

### Step 4: Test Verification

After saving the configuration, ask the user whether they want to send a test message.

**Bot Mode Test**

Ask the user:
> Please provide your Feishu open_id (starting with `ou_`) and I will send you a test message.
> How to get it: Open https://open.feishu.cn/api-explorer/ → select "Messages" on the left → "Message Management" → "Send Message" → get token → set query parameter `receive_id_type` to `open_id` → click "Quick copy open_id".

After receiving the open_id, call `feishu(action=send, ...)`:
```json
{
  "action": "send",
  "receive_id": "<user's open_id>",
  "receive_id_type": "open_id",
  "text": "🎉 Feishu bot configured successfully! You can now start chatting with me via Feishu."
}
```

If the send succeeds, inform the user that configuration is complete.
If the send fails, display the error message and prompt the user to check:
1. Whether the App ID and App Secret are correct
2. Whether the app has been published (Developer Console → Version Management & Release)
3. Whether the app has the "Get User Info" and "Send Messages" permissions

**Webhook Mode Test**

Call `feishu(action=webhook, ...)` directly:
```json
{
  "action": "webhook",
  "webhook_url": "<saved webhook_url>",
  "text": "🎉 Feishu Webhook configured successfully! I can now send messages to this group."
}
```

---

### Step 5: Completion

After successful configuration, summarize for the user:

**Bot Mode Completion Message**:
> ✅ Feishu bot configuration complete!
>
> You can now send messages to me directly in Feishu and I will receive and reply. Supported:
> - Direct chat (private message the bot)
> - Group chat (@mention the bot)
>
> Chat history and the Web interface are managed independently and do not interfere with each other.

**Webhook Mode Completion Message**:
> ✅ Feishu Webhook configuration complete!
>
> I can now proactively send messages to your Feishu group. To also receive Feishu messages, you can upgrade to Bot API mode.

---

## Notes

- The App Secret is stored encrypted using AES-GCM, with the key sourced from the server environment variable `FEISHU_ENCRYPT_KEY`
- If `FEISHU_ENCRYPT_KEY` is not configured, `feishu(action=set_config)` will return an error; prompt the user to contact an administrator to set this environment variable
- After the long-connection is started, no HTTP callback address is needed; the bot connects to the Feishu server proactively
- Each user can independently configure their own Feishu bot
