# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Approval displays of the local file and shell tools.

A file write's display names its workspace path and carries a unified diff
against the file as it is when the model asks; a shell command's names the
working directory and the full command. The TypeScript CLI core builds the
same text.
"""

from __future__ import annotations

import os
import re
from dataclasses import dataclass

from dex_agents import MAX_APPROVAL_PREVIEW_BYTES, AgentInterruptDisplay, is_credential_like_name
from dex_agents_cli_core.local_tools.files import WorkspaceFiles

#: Largest file, and largest new content, a file write preview diffs: 64 KiB.
MAX_PREVIEW_INPUT_BYTES = 64 * 1024
#: Most lines a file write preview has, its two header lines included.
MAX_PREVIEW_LINES = 200
_CONTEXT_LINES = 3
_CREDENTIAL_FILE_NAMES = frozenset(
    (".netrc", ".npmrc", ".pypirc", ".pgpass", "credentials", ".git-credentials")
)
_CREDENTIAL_FILE_EXTENSIONS = frozenset((".pem", ".key", ".p12", ".pfx", ".jks", ".keystore"))
_LEADING_DOTS = re.compile(r"^\.+")


def build_write_file_display(
    files: WorkspaceFiles, path: str, content: str
) -> AgentInterruptDisplay:
    """Return a ``write_file`` call's path, size, and diff against the file now."""
    source = files.read_text_for_preview(path, MAX_PREVIEW_INPUT_BYTES)
    target = path if source is None else source[0]
    return AgentInterruptDisplay(
        target=target,
        summary=f"Writes {_describe_bytes(content)} to {target}.",
        preview=None if source is None else _file_preview(source[0], source[1], content),
    )


def build_edit_file_display(
    files: WorkspaceFiles, path: str, old_text: str, new_text: str
) -> AgentInterruptDisplay:
    """Return an ``edit_file`` call's path, sizes, and diff when ``old_text`` occurs once."""
    source = files.read_text_for_preview(path, MAX_PREVIEW_INPUT_BYTES)
    target = path if source is None else source[0]
    current = None if source is None else source[1]
    preview = None
    if source is not None and current is not None and old_text and current.count(old_text) == 1:
        preview = _file_preview(source[0], current, current.replace(old_text, new_text, 1))
    return AgentInterruptDisplay(
        target=target,
        summary=(
            f"Replaces {_describe_bytes(old_text)} with {_describe_bytes(new_text)} in {target}."
        ),
        preview=preview,
    )


def build_shell_command_display(workspace: str, command: str) -> AgentInterruptDisplay:
    """Return a ``run_shell_command`` call's working directory and full command."""
    return AgentInterruptDisplay(
        target=workspace, summary=f"Runs a shell command in {workspace}: {command}"
    )


def is_credential_file_name(path: str) -> bool:
    """Report whether a file's name says it holds credentials, so a preview shows only its size.

    Such names are ``.env``, ``.env.local``, ``id_ed25519``, ``server.pem``,
    and ``api_key.txt``, for example.
    """
    name = os.path.basename(path).lower()
    stem, extension = os.path.splitext(name)
    return (
        name == ".env"
        or name.startswith(".env.")
        or name.startswith("id_")
        or name in _CREDENTIAL_FILE_NAMES
        or extension in _CREDENTIAL_FILE_EXTENSIONS
        or is_credential_like_name(_LEADING_DOTS.sub("", stem))
    )


def _describe_bytes(text: str) -> str:
    size = len(text.encode("utf-8"))
    return "1 byte" if size == 1 else f"{size} bytes"


def _file_preview(display_path: str, current_text: str | None, new_text: str) -> str | None:
    if (
        is_credential_file_name(display_path)
        or len(new_text.encode("utf-8")) > MAX_PREVIEW_INPUT_BYTES
    ):
        return None
    diff = _unified_diff(display_path, current_text, new_text)
    if diff is None or len(diff.encode("utf-8")) > MAX_APPROVAL_PREVIEW_BYTES:
        return None
    return diff


@dataclass(frozen=True)
class _DiffLine:
    kind: str
    text: str


def _unified_diff(display_path: str, current_text: str | None, new_text: str) -> str | None:
    """Return a diff with 3 lines of context, or ``None`` over ``MAX_PREVIEW_LINES`` lines."""
    old_lines = [] if current_text is None else _split_lines(current_text)
    new_lines = _split_lines(new_text)
    edits = _diff_lines(old_lines, new_lines, MAX_PREVIEW_LINES)
    if edits is None:
        return None
    output = [
        "--- /dev/null (no file now)"
        if current_text is None
        else f"--- {display_path} (the file now)",
        f"+++ {display_path} (after this write)",
    ]
    for header, lines in _group_hunks(edits):
        output.append(header)
        output.extend(f"{line.kind}{line.text}" for line in lines)
        if len(output) > MAX_PREVIEW_LINES:
            return None
    return "\n".join(output) + "\n"


def _split_lines(text: str) -> list[str]:
    if not text:
        return []
    lines = text.split("\n")
    if lines[-1] == "":
        lines.pop()
    return lines


def _diff_lines(
    old_lines: list[str], new_lines: list[str], maximum_edits: int
) -> list[_DiffLine] | None:
    """Return Myers' shortest edit script, or ``None`` past ``maximum_edits`` changes."""
    old_count = len(old_lines)
    new_count = len(new_lines)
    limit = min(old_count + new_count, maximum_edits)
    offset = limit + 1
    furthest = [0] * (2 * limit + 3)
    trace: list[list[int]] = []
    for edits in range(limit + 1):
        trace.append(list(furthest))
        for diagonal in range(-edits, edits + 1, 2):
            is_down = diagonal == -edits or (
                diagonal != edits
                and furthest[diagonal - 1 + offset] < furthest[diagonal + 1 + offset]
            )
            old_index = (
                furthest[diagonal + 1 + offset] if is_down else furthest[diagonal - 1 + offset] + 1
            )
            new_index = old_index - diagonal
            while (
                old_index < old_count
                and new_index < new_count
                and old_lines[old_index] == new_lines[new_index]
            ):
                old_index += 1
                new_index += 1
            furthest[diagonal + offset] = old_index
            if old_index >= old_count and new_index >= new_count:
                return _backtrack(trace, old_lines, new_lines, offset)
    return None


def _backtrack(
    trace: list[list[int]], old_lines: list[str], new_lines: list[str], offset: int
) -> list[_DiffLine]:
    lines: list[_DiffLine] = []
    old_index = len(old_lines)
    new_index = len(new_lines)
    for edits in range(len(trace) - 1, -1, -1):
        furthest = trace[edits]
        diagonal = old_index - new_index
        is_down = diagonal == -edits or (
            diagonal != edits and furthest[diagonal - 1 + offset] < furthest[diagonal + 1 + offset]
        )
        previous_diagonal = diagonal + 1 if is_down else diagonal - 1
        previous_old = furthest[previous_diagonal + offset]
        previous_new = previous_old - previous_diagonal
        while old_index > previous_old and new_index > previous_new:
            old_index -= 1
            new_index -= 1
            lines.append(_DiffLine(" ", old_lines[old_index]))
        if edits > 0:
            if is_down:
                new_index -= 1
                lines.append(_DiffLine("+", new_lines[new_index]))
            else:
                old_index -= 1
                lines.append(_DiffLine("-", old_lines[old_index]))
    lines.reverse()
    return lines


def _group_hunks(lines: list[_DiffLine]) -> list[tuple[str, list[_DiffLine]]]:
    """Group the edit script into hunks with 3 lines of context, as ``diff -u`` does."""
    changed = [index for index, line in enumerate(lines) if line.kind != " "]
    hunks: list[tuple[str, list[_DiffLine]]] = []
    position = 0
    while position < len(changed):
        last = changed[position]
        end = position + 1
        while end < len(changed) and changed[end] - last <= 2 * _CONTEXT_LINES + 1:
            last = changed[end]
            end += 1
        start = max(0, changed[position] - _CONTEXT_LINES)
        stop = min(len(lines), last + _CONTEXT_LINES + 1)
        before = lines[:start]
        hunk_lines = lines[start:stop]
        old_start = sum(1 for line in before if line.kind != "+")
        new_start = sum(1 for line in before if line.kind != "-")
        old_length = sum(1 for line in hunk_lines if line.kind != "+")
        new_length = sum(1 for line in hunk_lines if line.kind != "-")
        header = (
            f"@@ -{_format_range(old_start, old_length)} "
            f"+{_format_range(new_start, new_length)} @@"
        )
        hunks.append((header, hunk_lines))
        position = end
    return hunks


def _format_range(start: int, length: int) -> str:
    if length == 1:
        return str(start + 1)
    return f"{start if length == 0 else start + 1},{length}"
