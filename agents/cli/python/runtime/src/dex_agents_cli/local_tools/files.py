# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""File tools confined to the workspace."""

from __future__ import annotations

import os
import re
from pathlib import Path

from dex_agents import ToolExecutionError

#: The most text ``read_file`` and ``search_files`` return.
MAX_FILE_TOOL_OUTPUT_CHARACTERS = 100_000
_MAX_SEARCH_MATCHES = 200
_MAX_LISTED_ENTRIES = 1000


class WorkspaceFiles:
    """Read and change files, refusing every path that resolves outside the workspace.

    A path is resolved with symlinks followed, so ``..`` and a symlink that
    points out of the workspace are both refused.
    """

    def __init__(self, workspace: Path) -> None:
        """Confine the tools to one directory.

        Args:
            workspace: The workspace directory, which must exist.
        """
        self._root = workspace.resolve(strict=True)

    def read_file(self, path: str, offset: int = 0, limit: int | None = None) -> str:
        """Return a text file's lines.

        Args:
            path: Path relative to the workspace.
            offset: Zero-based first line.
            limit: Most lines to return, or ``None`` for all.

        Returns:
            The lines, cut to ``MAX_FILE_TOOL_OUTPUT_CHARACTERS``.
        """
        target = self.resolve(path)
        if not target.is_file():
            raise ToolExecutionError(f"{path} is not a file in the workspace")
        try:
            lines = target.read_text(encoding="utf-8", errors="replace").splitlines(keepends=True)
        except OSError as error:
            raise ToolExecutionError(f"cannot read {path}: {error.strerror}") from error
        selected = lines[offset : None if limit is None else offset + limit]
        return _truncate("".join(selected))

    def write_file(self, path: str, content: str) -> str:
        """Replace a file's content, creating it and its directories when needed.

        Args:
            path: Path relative to the workspace.
            content: The complete new text.

        Returns:
            What was written.
        """
        target = self.resolve(path)
        if target.is_dir():
            raise ToolExecutionError(f"{path} is a directory")
        try:
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(content, encoding="utf-8")
        except OSError as error:
            raise ToolExecutionError(f"cannot write {path}: {error.strerror}") from error
        return f"wrote {len(content.encode('utf-8'))} bytes to {self._relative(target)}"

    def edit_file(self, path: str, old_text: str, new_text: str) -> str:
        """Replace the one occurrence of a text in a file.

        Args:
            path: Path relative to the workspace.
            old_text: Text that occurs exactly once.
            new_text: Its replacement.

        Returns:
            What was changed.
        """
        target = self.resolve(path)
        if not target.is_file():
            raise ToolExecutionError(f"{path} is not a file in the workspace")
        text = target.read_text(encoding="utf-8")
        count = text.count(old_text) if old_text else 0
        if count != 1:
            raise ToolExecutionError(f"old_text occurs {count} times in {path}; it must occur once")
        target.write_text(text.replace(old_text, new_text, 1), encoding="utf-8")
        return f"edited {self._relative(target)}"

    def list_directory(self, path: str = ".") -> str:
        """List a directory's entries, one per line, directories ending in ``/``.

        Args:
            path: Directory relative to the workspace.

        Returns:
            The sorted entries.
        """
        target = self.resolve(path)
        if not target.is_dir():
            raise ToolExecutionError(f"{path} is not a directory in the workspace")
        entries = sorted(target.iterdir(), key=lambda entry: entry.name)
        lines = [entry.name + ("/" if entry.is_dir() else "") for entry in entries]
        if len(lines) > _MAX_LISTED_ENTRIES:
            lines = [*lines[:_MAX_LISTED_ENTRIES], f"... {len(lines) - _MAX_LISTED_ENTRIES} more"]
        return "\n".join(lines) if lines else "(empty)"

    def search_files(self, pattern: str, path: str = ".") -> str:
        """Find lines that match a regular expression in the files under a directory.

        Symlinks are not followed, so the search never leaves the workspace.

        Args:
            pattern: A Python regular expression.
            path: Directory relative to the workspace.

        Returns:
            ``<path>:<line>: <text>`` per match, at most 200.
        """
        try:
            expression = re.compile(pattern)
        except re.error as error:
            raise ToolExecutionError(f"invalid pattern: {error}") from error
        start = self.resolve(path)
        matches: list[str] = []
        for directory, directory_names, file_names in os.walk(start):
            directory_names.sort()
            for file_name in sorted(file_names):
                file_path = Path(directory) / file_name
                if file_path.is_symlink() or not file_path.is_file():
                    continue
                try:
                    lines = file_path.read_text(encoding="utf-8").splitlines()
                except (OSError, UnicodeDecodeError):
                    continue
                for number, line in enumerate(lines, start=1):
                    if expression.search(line):
                        matches.append(f"{self._relative(file_path)}:{number}: {line}")
                        if len(matches) >= _MAX_SEARCH_MATCHES:
                            return _truncate("\n".join(matches))
        return _truncate("\n".join(matches)) if matches else "no matches"

    def resolve(self, path: str) -> Path:
        """Return a workspace path's real location, refusing one outside the workspace.

        Args:
            path: Path relative to the workspace, or absolute inside it.

        Returns:
            The resolved path.

        Raises:
            ToolExecutionError: If the path resolves outside the workspace.
        """
        if "\x00" in path:
            raise ToolExecutionError("the path contains a NUL character")
        candidate = Path(path)
        if not candidate.is_absolute():
            candidate = self._root / candidate
        # resolve() follows every symlink that exists, so an escape through one is caught.
        resolved = candidate.resolve(strict=False)
        if resolved != self._root and not resolved.is_relative_to(self._root):
            raise ToolExecutionError(f"{path} is outside the workspace {self._root}")
        return resolved

    def _relative(self, path: Path) -> str:
        return str(path.relative_to(self._root)) if path != self._root else "."


def _truncate(text: str) -> str:
    if len(text) <= MAX_FILE_TOOL_OUTPUT_CHARACTERS:
        return text
    omitted = len(text) - MAX_FILE_TOOL_OUTPUT_CHARACTERS
    return f"{text[:MAX_FILE_TOOL_OUTPUT_CHARACTERS]}\n... ({omitted} more characters)"
