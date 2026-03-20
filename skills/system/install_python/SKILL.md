==============================
skill_id: install_python
name: Install Python Environment
display_name: Environment Setup
enable: true
description: Checks whether Python 3 is installed on the server; if not, prefers Homebrew for installation and falls back to pyenv to install the latest stable version when Homebrew is unavailable
trigger: Triggered when the user says "install python", "install python3", "set up python environment", "python not installed", or "help me install python"
==============================

## Execution Steps

### Step 1: Notify the User

Call `notify(action=progress)` with the message: "Checking Python environment..."

---

### Step 2: Run the Detection and Installation Script

Call `skill(action=run_script, skill_id="install_python", script_name="install.sh")`.

Script: outputs version if already installed; prefers `brew install python`, falls back to pyenv; verifies python3/pip3 after.

---

### Step 3: Report the Result to the User

Inform the user based on the script output:
- Already installed or installation succeeded: report the Python and pip versions and indicate which method was used
- Installation failed: explain the error reason and advise whether "install brew" needs to be run first or whether permissions need to be handled manually
