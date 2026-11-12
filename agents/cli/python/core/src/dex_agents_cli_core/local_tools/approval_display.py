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
working directory and the command with its inline credentials redacted. The
TypeScript CLI core builds the same text; the shared cases are in
``agents/conformance/approval_display.json``.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

from dex_agents import (
    MAX_APPROVAL_PREVIEW_BYTES,
    AgentInterruptDisplay,
    redact_inline_credentials,
)
from dex_agents_cli_core.local_tools.files import WorkspaceFiles

#: Largest file, and largest new content, a file write preview diffs: 64 KiB.
MAX_PREVIEW_INPUT_BYTES = 64 * 1024
#: Most lines a file write preview has, its header and marker lines included.
MAX_PREVIEW_LINES = 200
_CONTEXT_LINES = 3
_NO_NEWLINE_MARKER = "\\ No newline at end of file"
_CONTROL_CHARACTER = re.compile(r"[\x00-\x1f\x7f]")
_CREDENTIAL_DIRECTORIES = frozenset((".ssh", ".aws", ".kube", ".gnupg", ".docker", "gcloud"))
_CREDENTIAL_FILE_NAMES = frozenset(
    (
        ".netrc",
        ".npmrc",
        ".pypirc",
        ".pgpass",
        ".git-credentials",
        ".htpasswd",
        ".dockercfg",
        "credentials",
        "kubeconfig",
    )
)
_CREDENTIAL_FILE_EXTENSIONS = frozenset(
    (".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".kubeconfig", ".ppk", ".gpg", ".asc")
)
_CREDENTIAL_STEM_WORDS = frozenset(
    (
        "apikey",
        "apikeys",
        "cred",
        "creds",
        "credential",
        "credentials",
        "passwd",
        "password",
        "passwords",
        "privatekey",
        "secret",
        "secrets",
        "token",
        "tokens",
    )
)
_CREDENTIAL_STEM_ENDINGS = (
    "apikey",
    "credentials",
    "password",
    "passwd",
    "secret",
    "secrets",
    "token",
)
_CAMEL_CASE_BOUNDARY = re.compile(r"(?<=[a-z0-9])(?=[A-Z])")
_WORD_SEPARATOR = re.compile(r"[^a-z0-9]+")


def build_write_file_display(
    files: WorkspaceFiles, path: str, content: str
) -> AgentInterruptDisplay:
    """Return a ``write_file`` call's path, size, and diff against the file now."""
    source = files.read_text_for_preview(path, MAX_PREVIEW_INPUT_BYTES)
    target = _describe_path(path if source is None else source[0])
    return AgentInterruptDisplay(
        target=target,
        summary=f"Writes {_describe_bytes(content)} to {target}.",
        preview=None if source is None else build_file_preview(source[0], source[1], content),
    )


def build_edit_file_display(
    files: WorkspaceFiles, path: str, old_text: str, new_text: str
) -> AgentInterruptDisplay:
    """Return an ``edit_file`` call's path, sizes, and diff when ``old_text`` occurs once."""
    source = files.read_text_for_preview(path, MAX_PREVIEW_INPUT_BYTES)
    target = _describe_path(path if source is None else source[0])
    current = None if source is None else source[1]
    preview = None
    if source is not None and current is not None and old_text and current.count(old_text) == 1:
        preview = build_file_preview(source[0], current, current.replace(old_text, new_text, 1))
    return AgentInterruptDisplay(
        target=target,
        summary=(
            f"Replaces {_describe_bytes(old_text)} with {_describe_bytes(new_text)} in {target}."
        ),
        preview=preview,
    )


def build_shell_command_display(workspace: str, command: str) -> AgentInterruptDisplay:
    """Return a ``run_shell_command`` call's working directory and command.

    The command's inline credentials are redacted and its control characters
    escaped; the full command stays in the call's arguments. The session cuts a summary over
    ``MAX_APPROVAL_SUMMARY_BYTES``.
    """
    directory = _describe_path(workspace)
    return AgentInterruptDisplay(
        target=directory,
        summary=(
            f"Runs a shell command in {directory}: "
            f"{escape_control_characters(redact_inline_credentials(command))}"
        ),
    )


def _describe_path(path: str) -> str:
    """Return a path as displays show it.

    Each component's inline credentials are redacted, as in a ``password=…``
    name, and control characters are escaped.
    """
    return escape_control_characters(
        "/".join(redact_inline_credentials(part) for part in path.split("/"))
    )


def escape_control_characters(text: str) -> str:
    """Return the text with each control character written as ``\\u00XX``.

    A path with a line break then stays on one line of the preview.
    """
    return _CONTROL_CHARACTER.sub(lambda match: f"\\u{ord(match.group(0)):04x}", text)


def is_credential_file_name(path: str) -> bool:
    """Report whether a file's path says it holds credentials, so a preview shows only its size.

    Such paths are any file in ``.ssh``, ``.aws``, ``.kube``, ``.gnupg``,
    ``.docker``, or ``gcloud``; ``.env``, ``.env.local``, and ``prod.env``;
    ``.netrc``, ``.npmrc``, ``.pypirc``, ``.pgpass``, ``.git-credentials``,
    ``.htpasswd``, ``.dockercfg``, ``credentials``, and ``kubeconfig``; names
    that start with ``id_``; the extensions ``.pem``, ``.key``, ``.p12``,
    ``.pfx``, ``.jks``, ``.keystore``, ``.kubeconfig``, ``.ppk``, ``.gpg``, and
    ``.asc``; and names whose part before the first dot has a word such as
    ``secret``, ``credentials``, ``token``, ``password``, or ``api key``, or
    a word ending in one, such as ``secrets.yaml``, ``client_secret.json``,
    or ``githubtoken.txt``.
    """
    parts = [part for part in re.split(r"[/\\]", path) if part]
    if not parts:
        return False
    if any(part.lower() in _CREDENTIAL_DIRECTORIES for part in parts[:-1]):
        return True
    name = parts[-1].lower()
    extension = name[name.rfind(".") :] if name.rfind(".") > 0 else ""
    stem = parts[-1].lstrip(".").split(".", 1)[0]
    stem_words = [
        word for word in _WORD_SEPARATOR.split(_CAMEL_CASE_BOUNDARY.sub("_", stem).lower()) if word
    ]
    return (
        name == ".env"
        or name.startswith(".env.")
        or name.endswith(".env")
        or name.startswith("id_")
        or name in _CREDENTIAL_FILE_NAMES
        or extension in _CREDENTIAL_FILE_EXTENSIONS
        or any(
            word in _CREDENTIAL_STEM_WORDS or word.endswith(_CREDENTIAL_STEM_ENDINGS)
            for word in stem_words
        )
        or _has_words(stem_words, ("private", "key"))
        or _has_words(stem_words, ("api", "key"))
    )


def build_file_preview(display_path: str, current_text: str | None, new_text: str) -> str | None:
    """Return the labelled diff of a file write, or ``None`` when it must not be shown.

    It is ``None`` for a credential file, new content over
    ``MAX_PREVIEW_INPUT_BYTES``, either side with an inline credential (see
    ``redact_inline_credentials``), and a diff over ``MAX_PREVIEW_LINES``
    lines or ``MAX_APPROVAL_PREVIEW_BYTES`` bytes.
    """
    if (
        is_credential_file_name(display_path)
        or len(new_text.encode("utf-8", "surrogatepass")) > MAX_PREVIEW_INPUT_BYTES
        or _has_inline_credential(new_text)
        or (current_text is not None and _has_inline_credential(current_text))
    ):
        return None
    diff = _unified_diff(_describe_path(display_path), current_text, new_text)
    if diff is None or len(diff.encode("utf-8", "surrogatepass")) > MAX_APPROVAL_PREVIEW_BYTES:
        return None
    return diff


def _has_inline_credential(text: str) -> bool:
    return redact_inline_credentials(text) != text


def _has_words(words: list[str], phrase: tuple[str, ...]) -> bool:
    return any(tuple(words[index : index + len(phrase)]) == phrase for index in range(len(words)))


def _describe_bytes(text: str) -> str:
    size = len(text.encode("utf-8", "surrogatepass"))
    return "1 byte" if size == 1 else f"{size} bytes"


@dataclass(frozen=True)
class _Line:
    """One line of a file side; only a last line can lack its newline."""

    text: str
    is_missing_newline: bool


@dataclass(frozen=True)
class _DiffLine:
    kind: str
    line: _Line


def _unified_diff(header_path: str, current_text: str | None, new_text: str) -> str | None:
    """Return a diff with 3 lines of context, or ``None`` over ``MAX_PREVIEW_LINES`` lines."""
    old_lines = [] if current_text is None else _split_lines(current_text)
    new_lines = _split_lines(new_text)
    edits = _diff_lines(old_lines, new_lines, MAX_PREVIEW_LINES)
    if edits is None:
        return None
    output = [
        "--- /dev/null (no file now)"
        if current_text is None
        else f"--- {header_path} (the file now)",
        f"+++ {header_path} (after this write)",
    ]
    for header, lines in _group_hunks(edits):
        output.append(header)
        for line in lines:
            output.append(f"{line.kind}{line.line.text}")
            if line.line.is_missing_newline:
                output.append(_NO_NEWLINE_MARKER)
        if len(output) > MAX_PREVIEW_LINES:
            return None
    diff = "\n".join(output) + "\n"
    return diff if diff.count("\n") <= MAX_PREVIEW_LINES else None


def _split_lines(text: str) -> list[_Line]:
    if not text:
        return []
    parts = text.split("\n")
    is_missing_newline = parts[-1] != ""
    if not is_missing_newline:
        parts.pop()
    return [
        _Line(part, is_missing_newline and index == len(parts) - 1)
        for index, part in enumerate(parts)
    ]


def _diff_lines(
    old_lines: list[_Line], new_lines: list[_Line], maximum_edits: int
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
    trace: list[list[int]], old_lines: list[_Line], new_lines: list[_Line], offset: int
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
