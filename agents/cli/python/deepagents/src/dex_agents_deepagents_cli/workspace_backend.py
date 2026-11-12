# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Deep Agents backend of the command: real files in the workspace and its shell."""

from __future__ import annotations

import contextlib
import dataclasses
import hashlib
import logging
import os
import shutil
import stat
import time
from collections.abc import Iterator
from contextvars import ContextVar
from datetime import timedelta
from pathlib import Path

from deepagents.backends import CompositeBackend, FilesystemBackend, LocalShellBackend
from deepagents.backends.protocol import (
    DEFAULT_GREP_TIMEOUT,
    BackendProtocol,
    GlobResult,
    GrepResult,
    LsResult,
)
from deepagents.backends.utils import compile_grep_include_glob

#: The prefixes Deep Agents' ``FilesystemMiddleware`` writes its own files under.
DEEP_AGENTS_ARTIFACT_PREFIXES = ("/large_tool_results/", "/conversation_history/")

#: How long a session's artifacts stay after their last change.
SESSION_ARTIFACTS_RETENTION = timedelta(days=30)

_LOGGER = logging.getLogger(__name__)

_RUNNING_SESSION_ID: ContextVar[str | None] = ContextVar(
    "dex_agents_deepagents_running_session_id", default=None
)

# Search results keyed by path, each with its matching (line number, text) pairs.
_SearchResults = dict[str, list[tuple[int, str]]]


class WorkspaceShellBackend(LocalShellBackend):
    """A ``LocalShellBackend`` on the host's paths whose file tools stay in the workspace.

    File tools take absolute host paths, as Deep Agents' local mode does, so
    a file tool and ``execute`` name a file the same way. A path that
    resolves outside the workspace, through ``..`` or a symbolic link, fails
    the file operation with an error the model sees. ``ls``, ``glob``, and
    ``grep`` leave out every entry whose real path is outside the workspace,
    and ``grep`` never opens such a file.

    This is a guardrail against mistaken paths, not a sandbox. ``execute``
    runs ``/bin/sh`` in the workspace with the user's permissions and can
    reach any path. A link swapped during a call and a hard link to an
    outside file are not detected.
    """

    def __init__(self, workspace: Path) -> None:
        """Root the backend at the workspace.

        Args:
            workspace: The absolute workspace directory.

        Raises:
            RuntimeError: If the installed deepagents lacks the method the
                confinement overrides.
        """
        if not callable(getattr(LocalShellBackend, "_resolve_path", None)):
            # The confinement overrides this private method; the dependency range pins it.
            raise RuntimeError(
                "this deepagents release has no FilesystemBackend._resolve_path; install the "
                "version range dex-agents-deepagents-cli declares"
            )
        super().__init__(root_dir=workspace, virtual_mode=False, inherit_env=True)
        self._workspace_real_path = Path(os.path.realpath(workspace))

    def ls(self, path: str) -> LsResult:
        """List a directory, without the entries whose real path leaves the workspace."""
        result = super().ls(path)
        if result.entries is None:
            return result
        entries = [entry for entry in result.entries if self._is_inside(entry["path"])]
        return dataclasses.replace(result, entries=entries)

    def glob(self, pattern: str, path: str | None = None) -> GlobResult:
        """Find files, without the matches whose real path leaves the workspace."""
        result = super().glob(pattern, path)
        if result.matches is None:
            return result
        matches = [match for match in result.matches if self._is_inside(match["path"])]
        return dataclasses.replace(result, matches=matches)

    def grep(
        self,
        pattern: str,
        path: str | None = None,
        glob: str | None = None,
        *,
        max_count: int | None = None,
        context_lines: int = 0,
    ) -> GrepResult:
        """Search files, without the matches in files whose real path leaves the workspace."""
        result = super().grep(pattern, path, glob, max_count=max_count, context_lines=context_lines)
        if result.matches is None:
            return result
        matches = [match for match in result.matches if self._is_inside(match["path"])]
        return dataclasses.replace(result, matches=matches)

    def _python_search(
        self,
        pattern: str,
        base_full: Path,
        include_glob: str | None,
        *,
        max_count: int | None = None,
        timeout: int = DEFAULT_GREP_TIMEOUT,
    ) -> tuple[_SearchResults, bool, str | None]:
        # Deep Agents' fallback opens every file it walks; this one checks each real path first.
        deadline = time.monotonic() + timeout
        matches_glob = compile_grep_include_glob(include_glob) if include_glob else None
        root = base_full if base_full.is_dir() else base_full.parent
        candidates = (
            [base_full]
            if base_full.is_file()
            else (
                Path(directory) / name
                for directory, _, names in os.walk(root, followlinks=False)
                for name in names
            )
        )
        results: _SearchResults = {}
        unreadable: list[str] = []
        total = 0
        for candidate in candidates:
            if time.monotonic() > deadline:
                return results, True, None
            if matches_glob is not None and not matches_glob(
                candidate.relative_to(root).as_posix()
            ):
                continue
            if not self._is_inside(str(candidate)) or not _is_small_file(
                candidate, self.max_file_size_bytes
            ):
                continue
            try:
                with candidate.open(encoding="utf-8", errors="strict") as handle:
                    for line_number, line in enumerate(handle, 1):
                        if pattern not in line:
                            continue
                        if max_count is not None and total >= max_count:
                            return results, True, None
                        results.setdefault(str(candidate), []).append(
                            (line_number, line.rstrip("\n"))
                        )
                        total += 1
            except UnicodeDecodeError:
                # Binary files are skipped, as ripgrep does.
                continue
            except OSError as error:
                unreadable.append(f"- {candidate}: {type(error).__name__}: {error.strerror}")
        if not unreadable:
            return results, False, None
        return (
            results,
            False,
            "One or more files could not be fully searched:\n" + "\n".join(unreadable),
        )

    def _resolve_path(self, key: str) -> Path:
        resolved = super()._resolve_path(key)
        if not self._is_inside(str(resolved)):
            # An OSError, so each file operation reports it as its own error result.
            raise PermissionError(
                f"{key} is outside the workspace {self._workspace_real_path}; file tools "
                "may only use paths inside it"
            )
        return resolved

    def _is_inside(self, path: str) -> bool:
        # realpath also follows a dangling link to the target a write would create.
        real_path = Path(os.path.realpath(self.cwd / path))
        return real_path.is_relative_to(self._workspace_real_path)


class SessionArtifactsBackend(FilesystemBackend):
    """A virtual ``FilesystemBackend`` whose root is one private directory per session.

    Deep Agents names a large tool result after its tool call ID, which two
    sessions may share. Each session therefore gets its own directory,
    ``<artifacts>/<SHA-256 of the session ID>/<name>``. The name is stable,
    so a Worker that restarts on the same machine finds the files. Every
    file operation first checks each directory of that path: the user owns
    it, it is no symbolic link, and its mode becomes 0700 when it is looser.
    A failed check, or an operation outside a running model call or tool
    Step, returns the operation's error result.
    """

    def __init__(self, artifacts_directory: Path, name: str) -> None:
        """Keep the per-user directory and the name of this kind of file.

        Args:
            artifacts_directory: The private per-user directory, such as
                ``default_artifacts_directory()``.
            name: The subdirectory of each session, such as ``large_tool_results``.
        """
        self._artifacts_directory = artifacts_directory
        self._name = name
        super().__init__(root_dir=artifacts_directory, virtual_mode=True)

    @property
    def cwd(self) -> Path:
        """Return the running session's checked directory.

        Raises:
            PermissionError: Outside a running session, or when a directory of
                the path fails its check. Every file operation reports it as an error.
        """
        session_id = _RUNNING_SESSION_ID.get()
        if session_id is None:
            raise PermissionError("Deep Agents' own files are available only while a session runs")
        artifacts = ensure_artifacts_directory(self._artifacts_directory)
        session_directory = _private_directory(
            artifacts / hashlib.sha256(session_id.encode()).hexdigest()
        )
        # The virtual root check compares resolved paths.
        return _private_directory(session_directory / self._name).resolve()

    @cwd.setter
    def cwd(self, root_dir: Path) -> None:
        # FilesystemBackend's constructor assigns its root; each session's root replaces it.
        return None


@contextlib.contextmanager
def bind_artifacts_session(session_id: str) -> Iterator[None]:
    """Make Deep Agents' own files those of one session while the block runs.

    Args:
        session_id: The session ID, which is the Flow ID.
    """
    token = _RUNNING_SESSION_ID.set(session_id)
    try:
        yield
    finally:
        _RUNNING_SESSION_ID.reset(token)


def build_workspace_backend(
    workspace: Path, artifacts_directory: Path | None = None
) -> CompositeBackend:
    """Build the command's backend: the workspace, with Deep Agents' own files kept apart.

    Deep Agents writes large tool results and long user messages under
    ``/large_tool_results/`` and ``/conversation_history/``. Those prefixes go
    to the running session's private directory, so they never land at the
    file system root, in the workspace, or in another session.

    Args:
        workspace: The absolute workspace directory.
        artifacts_directory: The per-user directory of Deep Agents' own
            files; ``None`` uses ``default_artifacts_directory()``.

    Returns:
        The backend for ``create_deep_agent``.
    """
    artifacts = artifacts_directory or default_artifacts_directory()
    routes: dict[str, BackendProtocol] = {
        prefix: SessionArtifactsBackend(artifacts, prefix.strip("/"))
        for prefix in DEEP_AGENTS_ARTIFACT_PREFIXES
    }
    return CompositeBackend(default=WorkspaceShellBackend(workspace), routes=routes)


def default_artifacts_directory() -> Path:
    """Return the per-user directory of Deep Agents' own files, without creating it.

    Returns:
        ``~/.dex/agents/deepagents-artifacts``.
    """
    return Path.home() / ".dex" / "agents" / "deepagents-artifacts"


def ensure_artifacts_directory(artifacts_directory: Path) -> Path:
    """Create the per-user artifacts directory and check it and its two parents.

    The parents, such as ``~/.dex`` and ``~/.dex/agents``, keep their mode,
    but neither may be a symbolic link that another user owns.

    Args:
        artifacts_directory: The per-user directory.

    Returns:
        The directory, owned by the user, mode 0700.

    Raises:
        PermissionError: If a parent is another user's link, or the directory
            is a link or another user's.
    """
    for parent in (artifacts_directory.parent.parent, artifacts_directory.parent):
        if parent.is_symlink() and parent.lstat().st_uid != os.getuid():
            raise PermissionError(f"{parent} is a link that another user owns")
    artifacts_directory.parent.mkdir(parents=True, exist_ok=True)
    return _private_directory(artifacts_directory)


def remove_stale_session_artifacts(
    artifacts_directory: Path, retention: timedelta = SESSION_ARTIFACTS_RETENTION
) -> list[Path]:
    """Delete the session directories whose files nothing changed within the retention.

    The command calls it once when it starts. It never follows a link, and
    a directory it cannot read or delete stays and is logged.

    Args:
        artifacts_directory: The per-user directory.
        retention: How long after its last change a session's files stay.

    Returns:
        The deleted session directories.
    """
    if not artifacts_directory.is_dir() or artifacts_directory.is_symlink():
        return []
    cutoff = time.time() - retention.total_seconds()
    removed: list[Path] = []
    try:
        session_directories = list(artifacts_directory.iterdir())
    except OSError as error:
        _LOGGER.warning("cannot list session artifacts in %s: %s", artifacts_directory, error)
        return removed
    for session_directory in session_directories:
        try:
            if not stat.S_ISDIR(session_directory.lstat().st_mode):
                continue
            if _latest_change(session_directory) >= cutoff:
                continue
            shutil.rmtree(session_directory)
        except OSError as error:
            _LOGGER.warning(
                "cannot remove stale session artifacts %s: %s", session_directory, error
            )
            continue
        _LOGGER.info("removed stale session artifacts %s", session_directory)
        removed.append(session_directory)
    return removed


def _latest_change(directory: Path) -> float:
    latest = directory.lstat().st_mtime
    for parent, directory_names, file_names in os.walk(directory, followlinks=False):
        for name in (*directory_names, *file_names):
            latest = max(latest, (Path(parent) / name).lstat().st_mtime)
    return latest


def _private_directory(directory: Path) -> Path:
    directory.mkdir(mode=0o700, exist_ok=True)
    status = directory.lstat()
    if stat.S_ISLNK(status.st_mode) or not stat.S_ISDIR(status.st_mode):
        raise PermissionError(f"{directory} is not a directory")
    if status.st_uid != os.getuid():
        raise PermissionError(f"{directory} belongs to another user")
    if stat.S_IMODE(status.st_mode) & 0o077:
        directory.chmod(0o700)
    return directory


def _is_small_file(path: Path, max_bytes: int) -> bool:
    try:
        status = path.stat()
    except OSError:
        return False
    return stat.S_ISREG(status.st_mode) and status.st_size <= max_bytes
