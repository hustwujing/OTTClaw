==============================
skill_id: install_nodejs
name: Install Node.js
display_name: Environment Setup
enable: true
description: Checks whether Node.js is installed on the server; if not, prefers Homebrew for installation and falls back to nvm when Homebrew is unavailable
trigger: Triggered when the user says "install node", "install nodejs", "node not installed", "help me install node.js", or "set up node environment"
==============================

## Execution Steps

### Step 1: Notify the User

Call `notify(action=progress)` with the message: "Checking Node.js installation status..."

---

### Step 2: Run the Detection and Installation Script

Call `skill(action=run_script, skill_id="install_nodejs", script_name="install.sh")`.

Script: outputs version if already installed; prefers `brew install node`, falls back to nvm LTS; verifies node/npm after.

---

### Step 3: Report the Result to the User

Inform the user based on the script output:
- Already installed or installation succeeded: report the Node.js and npm versions and indicate which method was used
- Installation failed: explain the error reason and advise whether "install brew" needs to be run first or whether permissions need to be handled manually
