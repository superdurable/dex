#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

INLINE_BACKTICK = re.compile(r"`([^`\n]+)`")
NESTED_BOLD = re.compile(r"\*\*\*\*(.*?)\*\*\*\*")
NESTED_BOLD_PHRASE = re.compile(r"\*\*([A-Za-z][\w.-]*) \*\*([^*]+)\*\*\*\*")
SDK_DIRS = re.compile(r"(?:\*\*)?examples/\{([^}]+)\}(?:\*\*)?")
ANGLE_PLACEHOLDER = re.compile(r"(?:\*\*)?(/?(?:patterns|products)/)<name>(/\.\.\.)(?:\*\*)?")
STEP_ID_FORMAT = re.compile(r"(?:\*\*)?<StepType>-<Number>(?:\*\*)?")
CRON_EXPR = re.compile(r"\*\*0 \* \* \* \*\*\*")
MDX_UNSAFE = re.compile(r"[{}<>]")

DOC_ROOTS = (
    "docs/content",
    "docs/i18n/zh-Hans/docusaurus-plugin-content-docs/current",
)


def repo_root() -> Path:
    return Path(__file__).resolve().parent.parent


def doc_paths(root: Path) -> list[Path]:
    paths: list[Path] = []
    for relative in DOC_ROOTS:
        base = root / relative
        if base.is_dir():
            paths.extend(sorted(base.rglob("*.mdx")))
    return paths


def staged_doc_paths(root: Path) -> list[Path]:
    result = subprocess.run(
        ["git", "diff", "--cached", "--name-only", "--diff-filter=ACMR"],
        cwd=root,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        return []
    paths: list[Path] = []
    for line in result.stdout.splitlines():
        path = line.strip()
        if not path.endswith(".mdx"):
            continue
        if not any(path.startswith(prefix) for prefix in DOC_ROOTS):
            continue
        resolved = root / path
        if resolved.is_file():
            paths.append(resolved)
    return paths


def iter_prose_backticks(text: str) -> list[tuple[int, str]]:
    hits: list[tuple[int, str]] = []
    in_fence = False
    for line_number, line in enumerate(text.splitlines(), 1):
        stripped = line.strip()
        if stripped.startswith("```"):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        for match in INLINE_BACKTICK.finditer(line):
            hits.append((line_number, match.group(0)))
    return hits


def expand_sdk_dirs(match: re.Match[str]) -> str:
    names = [f"examples/{part.strip()}" for part in match.group(1).split(",")]
    if len(names) == 1:
        return names[0]
    return f"{', '.join(names[:-1])}, and {names[-1]}"


def replace_backtick(match: re.Match[str], line: str) -> str:
    inner = match.group(1)
    before = line[: match.start()]
    after = line[match.end() :]
    if before.endswith("**") and after.startswith("**"):
        return inner
    if inner == "<StepType>-<Number>":
        return "**StepType-Number**"
    if MDX_UNSAFE.search(inner):
        if inner.startswith("examples/{") and inner.endswith("}"):
            return expand_sdk_dirs(re.match(r"examples/\{([^}]+)\}", inner))
        return (
            inner.replace("<name>", "name")
            .replace("<StepType>", "StepType")
            .replace("<Number>", "Number")
            .replace("{", "")
            .replace("}", "")
        )
    return f"**{inner}**"


def repair_prose_line(line: str) -> str:
    updated = NESTED_BOLD_PHRASE.sub(r"**\1 \2**", line)
    updated = NESTED_BOLD.sub(r"**\1**", updated)
    updated = STEP_ID_FORMAT.sub("**StepType-Number**", updated)
    updated = ANGLE_PLACEHOLDER.sub(r"\1name\2", updated)
    updated = SDK_DIRS.sub(expand_sdk_dirs, updated)
    updated = CRON_EXPR.sub("0 \\* \\* \\* \\*", updated)
    return updated


def fix_text(text: str) -> str:
    lines: list[str] = []
    in_fence = False
    for line in text.splitlines(keepends=True):
        stripped = line.strip()
        if stripped.startswith("```"):
            in_fence = not in_fence
            lines.append(line)
            continue
        if in_fence:
            lines.append(line)
            continue
        newline = ""
        if line.endswith("\n"):
            newline = "\n"
            body = line[:-1]
            if body.endswith("\r"):
                newline = "\r\n"
                body = body[:-1]
        else:
            body = line

        def sub_backtick(match: re.Match[str]) -> str:
            return replace_backtick(match, body)

        body = INLINE_BACKTICK.sub(sub_backtick, body)
        body = repair_prose_line(body)
        lines.append(body + newline)
    return "".join(lines)


def check_paths(paths: list[Path], root: Path) -> int:
    violations = 0
    for path in paths:
        hits = iter_prose_backticks(path.read_text(encoding="utf-8"))
        if not hits:
            continue
        violations += len(hits)
        relative = path.relative_to(root)
        for line_number, token in hits:
            print(f"{relative}:{line_number}: inline backtick {token!r} in prose")
    return violations


def fix_paths(paths: list[Path]) -> int:
    changed = 0
    for path in paths:
        original = path.read_text(encoding="utf-8")
        updated = fix_text(original)
        if updated != original:
            path.write_text(updated, encoding="utf-8")
            changed += 1
    return changed


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Reject or fix inline backticks in product-doc MDX prose.",
    )
    parser.add_argument(
        "--fix",
        action="store_true",
        help="Replace inline backticks with **bold** outside fenced blocks.",
    )
    parser.add_argument(
        "--staged",
        action="store_true",
        help="Only inspect staged docs MDX files.",
    )
    parser.add_argument(
        "paths",
        nargs="*",
        help="Optional MDX paths relative to the repo root.",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    root = repo_root()
    if args.paths:
        paths = [root / path for path in args.paths]
    elif args.staged:
        paths = staged_doc_paths(root)
    else:
        paths = doc_paths(root)

    if args.fix:
        changed = fix_paths(paths)
        print(f"updated {changed} file(s)")
        return 0

    violations = check_paths(paths, root)
    if violations:
        print(
            f"docs prose must not use inline backticks ({violations} violation(s)); "
            "use **bold** instead. Run: python3 script/docs_no_inline_backticks.py --fix",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
