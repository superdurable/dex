# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Deep Agents backend of the command: real files in the workspace and its shell."""

from __future__ import annotations

import os
import tempfile
from pathlib import Path

from deepagents.backends import CompositeBackend, FilesystemBackend, LocalShellBackend
from deepagents.backends.protocol import BackendProtocol

#: The prefixes Deep Agents' ``FilesystemMiddleware`` writes its own files under.
DEEP_AGENTS_ARTIFACT_PREFIXES = ("/large_tool_results/", "/conversation_history/")


class WorkspaceShellBackend(LocalShellBackend):
    """A ``LocalShellBackend`` on the host's paths whose file operations stay in the workspace.

    File tools take absolute host paths, as Deep Agents' local mode does, so
    a file tool and ``execute`` name a file the same way. A path that
    resolves outside the workspace, through ``..`` or a symbolic link, fails
    the file operation with an error the model sees. ``execute`` runs
    ``/bin/sh`` in the workspace with the command's environment and can reach
    any path, as the CLI core's shell tool can.
    """

    def __init__(self, workspace: Path) -> None:
        """Root the backend at the workspace.

        Args:
            workspace: The absolute workspace directory.
        """
        super().__init__(root_dir=workspace, virtual_mode=False, inherit_env=True)
        self._workspace_real_path = Path(os.path.realpath(workspace))

    def _resolve_path(self, key: str) -> Path:
        resolved = super()._resolve_path(key)
        # realpath also follows a dangling link to the target a write would create.
        if not Path(os.path.realpath(resolved)).is_relative_to(self._workspace_real_path):
            # An OSError, so each file operation reports it as its own error result.
            raise PermissionError(
                f"{key} is outside the workspace {self._workspace_real_path}; file tools "
                "may only use paths inside it"
            )
        return resolved


def build_workspace_backend(
    workspace: Path, artifacts_directory: Path | None = None
) -> CompositeBackend:
    """Build the command's backend: the workspace, with Deep Agents' own files kept apart.

    Deep Agents writes large tool results and long user messages under
    ``/large_tool_results/`` and ``/conversation_history/``. Those prefixes go
    to private directories, so they never land at the file system root or in
    the workspace.

    Args:
        workspace: The absolute workspace directory.
        artifacts_directory: The directory of Deep Agents' own files;
            ``None`` uses ``dex-agents-deepagents-<user ID>`` in the temporary
            directory, readable only by the user.

    Returns:
        The backend for ``create_deep_agent``.
    """
    artifacts = artifacts_directory or default_artifacts_directory()
    routes: dict[str, BackendProtocol] = {}
    for prefix in DEEP_AGENTS_ARTIFACT_PREFIXES:
        directory = artifacts / prefix.strip("/")
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        routes[prefix] = FilesystemBackend(root_dir=directory, virtual_mode=True)
    return CompositeBackend(default=WorkspaceShellBackend(workspace), routes=routes)


def default_artifacts_directory() -> Path:
    """Return the private temporary directory of Deep Agents' own files, creating it.

    Returns:
        ``dex-agents-deepagents-<user ID>`` in the temporary directory, mode 0700.

    Raises:
        PermissionError: If the path is a symbolic link or another user owns it.
    """
    directory = Path(tempfile.gettempdir()) / f"dex-agents-deepagents-{os.getuid()}"
    directory.mkdir(mode=0o700, exist_ok=True)
    if directory.is_symlink() or directory.stat().st_uid != os.getuid():
        raise PermissionError(f"{directory} is not a directory of this user")
    # mkdir leaves an existing directory's mode unchanged.
    directory.chmod(0o700)
    return directory
