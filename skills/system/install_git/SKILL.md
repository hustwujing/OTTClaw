==============================
skill_id: install_git
name: Install Git
display_name: Install Git
enable: true
description: Detects and installs Git on the server, automatically identifies the operating system (Debian/Ubuntu, CentOS/RHEL, Alpine, macOS), and returns the Git version number after installation.
trigger: Triggered when the user requests to install Git, or when a Git-dependent operation is attempted and Git is not installed on the system
==============================

## Execution Steps

1. Call `skill(action=run_script, skill_id="install_git", script_name="scripts/install_git.sh")` with no additional args
2. The script automatically detects the operating system and selects the appropriate package manager to install Git
3. Return the script output (including the version number or error message) directly to the user

## Notes

- The script requires root or sudo privileges; if permissions are insufficient, the script will report an error and advise the user to retry as root
- If Git is already installed on the system, the script returns the current version immediately without reinstalling
