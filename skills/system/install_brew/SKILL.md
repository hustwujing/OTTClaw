==============================
skill_id: install_brew
name: Install Homebrew
display_name: Environment Setup
enable: true
description: Checks whether Homebrew is installed on the server, and automatically runs the official installation script if it is not
trigger: Triggered when the user says "install brew", "install homebrew", "brew not installed", or "help me set up brew"
==============================

## Execution Steps

### Step 1: Notify the User

Call `notify(action=progress)` with the message: "Checking Homebrew installation status..."

---

### Step 2: Run the Detection and Installation Script

Call `skill(action=run_script, skill_id="install_brew", script_name="install.sh")`.

Script: outputs version if already installed; otherwise runs official script with `NONINTERACTIVE=1` and verifies.

---

### Step 3: Report the Result to the User

Inform the user based on the script output:
- Installation succeeded or already installed: report the Homebrew version
- Installation failed: explain the error reason and advise the user that manual execution in a terminal may be required (the installation process requires sudo privileges; it will fail if the service process lacks sufficient permissions)
