==============================
skill_id: wecom_bot_setup
name: WeCom AI Bot Configuration Wizard
display_name: WeCom AI Bot Setup
enable: true
description: Guides users through binding a WeCom AI bot (Bot ID + Secret) for bidirectional long-connection messaging — receive and reply in real time
trigger: Triggered when the user says "configure WeCom AI bot", "bind WeCom bot", "set up WeCom AI bot", "WeCom bot configuration", "企业微信 AI 机器人", or similar requests
==============================

## Overview

WeCom AI Bot mode uses a persistent WebSocket connection to receive and reply to WeCom messages in real time. It requires a **Bot ID** and a **Bot Secret** issued by the WeCom platform.

This is different from the Webhook (group bot) mode, which can only push messages out.

---

## Steps

### Step 1: Check Config

Call `wecom(action=get_bot_config)`.

- If `configured: true`, show `bot_id` and ask: reconfigure or keep existing?
- If `configured: false`, proceed.

---

### Step 2: Collect Credentials

Explain where to find the credentials:

> **Bot ID** and **Bot Secret** are issued when you create an AI bot in the WeCom Admin Console:
> 1. Log in to WeCom Admin Console (https://work.weixin.qq.com/wework_admin/)
> 2. Go to **应用管理 → 应用 → AI 机器人**
> 3. Create or select a bot → the **Bot ID** (`botid_...`) and **Secret** are shown on the credentials page

Ask the user to paste the **Bot ID** (format: `botid_...`).

Then ask for the **Bot Secret** (keep it short in the display — it will be encrypted at rest).

---

### Step 3: Confirm

`notify(action=confirm)` showing:
- Bot ID (full)
- Secret (first 6 chars + `****`)

---

### Step 4: Save and Start

Call `wecom(action=set_bot_config, bot_id=<bot_id>, secret=<secret>)`.

This saves the credentials (Secret is AES-GCM encrypted) and immediately starts the WebSocket long-connection for this user.

On success, tell the user: "Credentials saved and bot connection started."

On error:
- `WECOM_ENCRYPT_KEY not configured` → ask admin to set `WECOM_ENCRYPT_KEY` env var and restart the server.
- Other errors → show the error and let user retry.

---

### Step 5: Verify

Explain how to test:

> Send a message to the AI bot in WeCom (direct chat or group @mention). If the bot replies, the connection is working.
>
> The bot connects proactively — no inbound HTTP or port forwarding is needed.

Ask if the user has tested it and whether it responded.

---

### Step 6: Done

"WeCom AI bot is ready. Messages sent to the bot in WeCom will be processed by the agent and replied to in real time. Web and WeCom histories are independent sessions."

---

## Notes

- Secret is encrypted with AES-GCM; key from env `WECOM_ENCRYPT_KEY`. If missing, `set_bot_config` returns an error — ask admin.
- The connection auto-reconnects with exponential backoff (up to 30 s) on disconnection.
- Each user binds their own bot independently; credentials are per-user.
- Single-chat messages are identified by `userid`; group messages by `chatid`.
- Supported message types: text, voice (transcribed), image placeholder, file placeholder.
