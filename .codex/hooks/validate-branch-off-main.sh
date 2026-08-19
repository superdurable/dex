#!/bin/sh
# Codex PreToolUse hook: require new branches to start from origin/main.
root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
exec python3 "$root/script/branch_off_main_policy.py" codex
