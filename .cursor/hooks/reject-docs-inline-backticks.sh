#!/bin/sh
# Cursor hook: block commits that leave inline backticks in product-doc prose.
root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
payload=$(mktemp)
trap 'rm -f "$payload"' EXIT
cat > "$payload"
python3 - "$payload" "$root" <<'PY'
from __future__ import annotations

import json
import re
import subprocess
import sys

GIT_COMMIT = re.compile(
    r"(?:^|[;&|\n]\s*)git\b(?:\s+(?:-[^\s]+|-C\s+\S+))*\s+commit\b"
)


def read_payload() -> dict:
    try:
        with open(sys.argv[1], encoding="utf-8") as handle:
            payload = json.load(handle)
    except Exception:
        return {}
    return payload if isinstance(payload, dict) else {}


def command_text(payload: dict) -> str:
    tool_input = payload.get("tool_input")
    if isinstance(tool_input, dict) and isinstance(tool_input.get("command"), str):
        return tool_input["command"]
    command = payload.get("command")
    return command if isinstance(command, str) else ""


def emit(payload: dict) -> None:
    json.dump(payload, sys.stdout, separators=(",", ":"))
    sys.stdout.write("\n")


def deny(message: str) -> None:
    emit(
        {
            "permission": "deny",
            "agent_message": message,
            "user_message": message,
        }
    )


def main() -> int:
    payload = read_payload()
    command = command_text(payload)
    if not GIT_COMMIT.search(command):
        emit({"permission": "allow"})
        return 0

    root = sys.argv[2]
    result = subprocess.run(
        [sys.executable, f"{root}/script/docs_no_inline_backticks.py", "--staged"],
        cwd=root,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        message = (result.stderr or result.stdout or "docs prose uses inline backticks").strip()
        deny(message)
        return 0

    emit({"permission": "allow"})
    return 0


if __name__ == "__main__":
    sys.exit(main())
PY
