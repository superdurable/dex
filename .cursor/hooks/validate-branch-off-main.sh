#!/bin/sh
# Cursor hook: require new branches to start from origin/main.
hook_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$hook_dir/../.." && pwd)
export DEX_VALIDATE_BRANCH_SCRIPT="$repo_root/script/validate-branch-off-main.sh"
payload=$(mktemp)
trap 'rm -f "$payload"' EXIT
cat > "$payload"
python3 - "$payload" <<'PY'
from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from pathlib import Path

CREATE_PATTERNS = (
    re.compile(
        r"(?:^|[;&|\n]\s*)git\b(?:\s+(?:-[^\s]+|-C\s+\S+))*\s+checkout\b"
        r"(?:\s+(?:-[^\s]+))*\s+-b\s+(\S+)(?:\s+(\S+))?",
        re.I,
    ),
    re.compile(
        r"(?:^|[;&|\n]\s*)git\b(?:\s+(?:-[^\s]+|-C\s+\S+))*\s+switch\b"
        r"\s+(?:-c|--create)\s+(\S+)(?:\s+(\S+))?",
        re.I,
    ),
    re.compile(
        r"(?:^|[;&|\n]\s*)git\b(?:\s+(?:-[^\s]+|-C\s+\S+))*\s+branch\b"
        r"(?!\s+-)(?:\s+(?!-[dDmM]|--(?:delete|move|list|contains|merged|show-current|set-upstream-to)\b)(?:-[^\s]+\s+)*)?(\S+)(?:\s+(\S+))?",
        re.I,
    ),
)
PROTECTED = {"main", "master"}


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


def validator_path() -> Path | None:
    env_path = os.environ.get("DEX_VALIDATE_BRANCH_SCRIPT")
    if env_path:
        path = Path(env_path)
        if path.is_file():
            return path
    root = repo_root()
    if root is None:
        return None
    path = root / "script" / "validate-branch-off-main.sh"
    return path if path.is_file() else None


def repo_root() -> Path | None:
    try:
        result = subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            check=False,
            capture_output=True,
            text=True,
        )
    except OSError:
        return None
    if result.returncode != 0:
        return None
    return Path(result.stdout.strip())


def parse_create(command: str) -> tuple[str, str | None] | None:
    for pattern in CREATE_PATTERNS:
        match = pattern.search(command)
        if not match:
            continue
        branch = match.group(1)
        start = match.group(2) if match.lastindex and match.lastindex >= 2 else None
        if branch.startswith("-") or branch in PROTECTED:
            return None
        return branch, start
    return None


def run_validate(validator: Path, mode: str, start_ref: str | None) -> tuple[bool, str]:
    args = [str(validator), mode]
    if start_ref is not None:
        args.append(start_ref)
    try:
        result = subprocess.run(
            args,
            check=False,
            capture_output=True,
            text=True,
        )
    except OSError as err:
        return False, f"Could not run branch validator: {err}"
    if result.returncode == 0:
        return True, ""
    message = (result.stderr or result.stdout or "").strip()
    return False, message or "Branch must start from origin/main."


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
    create = parse_create(command)
    if create is None:
        emit({"permission": "allow"})
        return 0

    root = repo_root()
    if root is None:
        emit({"permission": "allow"})
        return 0

    validator = validator_path()
    if validator is None:
        emit({"permission": "allow"})
        return 0

    _, start_ref = create
    ok, message = run_validate(
        validator,
        "validate-start" if start_ref else "validate-head",
        start_ref,
    )
    if ok:
        emit({"permission": "allow"})
        return 0

    deny(message)
    return 0


if __name__ == "__main__":
    sys.exit(main())
PY
