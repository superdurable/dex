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
import os
from collections.abc import Iterator
from contextvars import ContextVar
from pathlib import Path

from deepagents.backends import CompositeBackend, FilesystemBackend, LocalShellBackend
from deepagents.backends.protocol import BackendProtocol, GlobResult, GrepResult, LsResult

#: The prefixes Deep Agents' ``FilesystemMiddleware`` writes its own files under.
DEEP_AGENTS_ARTIFACT_PREFIXES = ("/large_tool_results/", "/conversation_history/")

_RUNNING_SESSION_ID: ContextVar[str | None] = ContextVar(
    "dex_agents_deepagents_running_session_id", default=None
)


class WorkspaceShellBackend(LocalShellBackend):
    """A ``LocalShellBackend`` on the host's paths whose file operations stay in the workspace.

    File tools take absolute host paths, as Deep Agents' local mode does, so
    a file tool and ``execute`` name a file the same way. A path that
    resolves outside the workspace, through ``..`` or a symbolic link, fails
    the file operation with an error the model sees. ``ls``, ``glob``, and
    ``grep`` also leave out every entry whose real path is outside the
    workspace, such as a link to a file elsewhere. ``execute`` runs
    ``/bin/sh`` in the workspace with the command's environment and can reach
    any path, as the CLI core's shell tool can.
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
    ``<artifacts>/<SHA-256 of the session ID>/<name>``, mode 0700. The name
    is stable, so a Worker that restarts on the same machine finds the files.
    A file operation outside a running model call or tool Step fails.
    """

    def __init__(self, artifacts_directory: Path, name: str) -> None:
        """Keep the per-user directory and the name of this kind of file.

        Args:
            artifacts_directory: The private per-user directory.
            name: The subdirectory of each session, such as ``large_tool_results``.
        """
        self._artifacts_directory = artifacts_directory.resolve()
        self._name = name
        super().__init__(root_dir=self._artifacts_directory, virtual_mode=True)

    @property
    def cwd(self) -> Path:
        """Return the running session's directory, creating it with mode 0700.

        Raises:
            RuntimeError: Outside a model call and a tool Step of a session.
        """
        session_id = _RUNNING_SESSION_ID.get()
        if session_id is None:
            raise RuntimeError("Deep Agents' own files are available only while a session runs")
        session_directory = (
            self._artifacts_directory / hashlib.sha256(session_id.encode()).hexdigest()
        )
        session_directory.mkdir(mode=0o700, exist_ok=True)
        directory = session_directory / self._name
        directory.mkdir(mode=0o700, exist_ok=True)
        return directory

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
    """Return the per-user directory of Deep Agents' own files, creating it.

    Returns:
        ``~/.dex/agents/deepagents-artifacts``, mode 0700.

    Raises:
        PermissionError: If the path is a symbolic link or another user owns it.
    """
    directory = Path.home() / ".dex" / "agents" / "deepagents-artifacts"
    directory.parent.mkdir(parents=True, exist_ok=True)
    directory.mkdir(mode=0o700, exist_ok=True)
    if directory.is_symlink() or directory.stat().st_uid != os.getuid():
        raise PermissionError(f"{directory} is not a directory of this user")
    # mkdir leaves an existing directory's mode unchanged.
    directory.chmod(0o700)
    return directory
