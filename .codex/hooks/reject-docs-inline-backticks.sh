#!/bin/sh
# Codex hook: block commits that leave inline backticks in product-doc prose.
root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
payload=$(mktemp)
trap 'rm -f "$payload"' EXIT
cat > "$payload" || true
python3 - "$payload" "$root" <<'PY'
from __future__ import annotations

import json
import re
import subprocess
import sys

GIT_COMMIT = re.compile(
    r"(?:^|[;&|\n]\s*)git\b(?:\s+(?:-[^\s]+|-C\s+\S+))*\s+commit\b"
)


def command_text(payload: dict) -> str:
    for key in ("command", "cmd"):
        value = payload.get(key)
        if isinstance(value, str):
            return value
    arguments = payload.get("arguments")
    if isinstance(arguments, dict) and isinstance(arguments.get("command"), str):
        return arguments["command"]
    tool_input = payload.get("tool_input")
    if isinstance(tool_input, dict) and isinstance(tool_input.get("command"), str):
        return tool_input["command"]
    return ""


try:
    with open(sys.argv[1], encoding="utf-8") as handle:
        raw = handle.read().strip()
    payload = json.loads(raw) if raw else {}
except Exception:
    sys.exit(0)

if not isinstance(payload, dict):
    sys.exit(0)

command = command_text(payload)
if not command or not GIT_COMMIT.search(command):
    sys.exit(0)

root = sys.argv[2]
result = subprocess.run(
    [sys.executable, f"{root}/script/docs_no_inline_backticks.py", "--staged"],
    cwd=root,
    check=False,
)
sys.exit(result.returncode)
PY
