==============================
skill_id: wecom_setup
name: WeCom Bot Configuration Wizard
display_name: WeCom Setup Assistant
enable: true
description: Guides users through configuring a WeCom group bot (Webhook mode), with support for test verification
trigger: Triggered when the user says "configure WeCom", "set up WeCom", "WeCom configuration", "connect WeCom", or similar requests
==============================

Webhook push-only; cannot receive messages.

## Steps

### Step 1: Check Config

`wecom(action=get_config)`. If already configured, show masked URL and ask to reconfigure. Otherwise proceed.

### Step 2: Collect Webhook URL

Ask user: WeCom group → "..." → Add Group Bot → Create bot → copy Webhook URL (`https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...`). Validate it starts with that prefix; if not, ask again.

### Step 3: Confirm and Save

`notify(action=confirm)` showing masked URL. After confirm: `wecom(action=set_config, webhook_url=...)`.

### Step 4: Test

Ask if user wants a test message. If yes: `wecom(action=send, text="WeCom bot configured!", msgtype="markdown")`. On failure, check: URL copied in full (includes `key=`), bot added to group, group/bot not removed.

### Step 5: Done

"WeCom bot configured. Use `msgtype: \"markdown\"` for formatted content."
