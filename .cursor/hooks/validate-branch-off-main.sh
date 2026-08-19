#!/bin/sh
# Cursor hook: require new branches to start from origin/main.
payload=$(mktemp)
trap 'rm -f "$payload"' EXIT
cat > "$payload"
python3 - "$payload" <<'PY'
from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path

MAIN_REMOTE = "origin"
MAIN_BRANCH = "main"
MAIN_REF = f"{MAIN_REMOTE}/{MAIN_BRANCH}"

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


def git_output(args: list[str], cwd: Path) -> str:
    result = subprocess.run(
        args,
        cwd=cwd,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        detail = (result.stderr or result.stdout or "git command failed").strip()
        raise RuntimeError(detail)
    return result.stdout.strip()


def fetch_main(cwd: Path) -> None:
    git_output(["git", "fetch", MAIN_REMOTE, MAIN_BRANCH, "--quiet"], cwd)


def validate_start_ref(start_ref: str, cwd: Path) -> tuple[bool, str]:
    try:
        fetch_main(cwd)
        main_sha = git_output(["git", "rev-parse", MAIN_REF], cwd)
        start_sha = git_output(["git", "rev-parse", start_ref], cwd)
    except RuntimeError as err:
        return False, str(err)
    if start_sha == main_sha:
        return True, ""
    return False, (
        f"Branch start {start_ref} ({start_sha}) is not {MAIN_REF} ({main_sha}).\n"
        f"Fetch and branch from {MAIN_REF}:\n"
        f"  git fetch {MAIN_REMOTE} {MAIN_BRANCH}\n"
        f"  git switch -c <branch> {MAIN_REF}"
    )


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

    _, start_ref = create
    ok, message = validate_start_ref(start_ref or "HEAD", root)
    if ok:
        emit({"permission": "allow"})
        return 0

    deny(message)
    return 0


if __name__ == "__main__":
    sys.exit(main())
PY
