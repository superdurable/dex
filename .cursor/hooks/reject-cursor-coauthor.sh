#!/bin/sh
# Cursor hook: reject git commits that attribute Cursor as a co-author.
payload=$(mktemp)
trap 'rm -f "$payload"' EXIT
cat > "$payload"
python3 - "$payload" <<'PY'
from __future__ import annotations

import json
import re
import subprocess
import sys

FORBIDDEN_IN_MESSAGE = re.compile(
    r"(?im)^\s*Co-authored-by:.*\bcursor\b"
    r"|cursoragent@cursor\.com"
    r"|^\s*Made-with:\s*Cursor\b"
)
GIT_COMMIT = re.compile(
    r"(?:^|[;&|\n]\s*)git\b(?:\s+(?:-[^\s]+|-C\s+\S+))*\s+commit\b"
)
SKIP_HOOKS = re.compile(r"(?:^|\s)(--no-verify|-n)(?:\s|$)")


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


def is_after_event(payload: dict) -> bool:
    event = str(payload.get("hook_event_name") or payload.get("event") or "")
    return event.startswith("after") or event.startswith("post") or "exit_code" in payload


def emit(payload: dict) -> None:
    json.dump(payload, sys.stdout, separators=(",", ":"))
    sys.stdout.write("\n")


def head_message() -> str:
    try:
        result = subprocess.run(
            ["git", "log", "-1", "--format=%B"],
            check=False,
            capture_output=True,
            text=True,
        )
    except OSError:
        return ""
    return result.stdout if result.returncode == 0 else ""


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

    if is_after_event(payload):
        if FORBIDDEN_IN_MESSAGE.search(head_message()):
            message = (
                "HEAD has a Cursor co-author trailer. Amend the message to "
                "remove it before pushing. Do not use --no-verify."
            )
            emit(
                {
                    "permission": "allow",
                    "agent_message": message,
                    "additional_context": message,
                }
            )
            return 0
        emit({"permission": "allow"})
        return 0

    if SKIP_HOOKS.search(command):
        deny("Do not skip git hooks. Re-run git commit without --no-verify or -n.")
        return 0

    emit({"permission": "allow"})
    return 0


if __name__ == "__main__":
    sys.exit(main())
PY
