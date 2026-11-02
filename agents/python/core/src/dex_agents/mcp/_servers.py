# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Worker-side configurations of the MCP servers a tool source connects to."""

from __future__ import annotations

import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from typing import TypeAlias

_SERVER_NAME_PATTERN = re.compile(r"[A-Za-z0-9_-]+")


@dataclass(frozen=True, kw_only=True)
class McpStdioServerConfig:
    """Launch one MCP server as a child process and talk to it over stdio.

    Build it from Worker configuration, never from Flow input, a user message,
    or a browser: the command runs with the Worker's privileges. Every request
    starts a new process, so a server that keeps state between calls must
    keep it outside the process, and its MCP tasks cannot outlive one tool
    Step attempt.

    Example:
        >>> McpStdioServerConfig(
        ...     name="files",
        ...     command="npx",
        ...     args=["-y", "@modelcontextprotocol/server-filesystem", "/srv/reports"],
        ... )

    Attributes:
        name: Server name, unique within one ``McpToolSource``. It prefixes the
            agent tool names, so it may contain only letters, digits, ``_``,
            and ``-``.
        command: Executable to run.
        args: Command-line arguments.
        env: Environment variables merged over the MCP SDK's safe default
            environment, which keeps only variables such as ``PATH`` and
            ``HOME``. ``None`` passes only the default environment.
        cwd: Working directory, or ``None`` for the Worker's.
        trusts_tool_annotations: Whether the server's ``readOnlyHint`` and
            ``idempotentHint`` annotations pick a tool's effect. Defaults to
            ``True``. A server with a wrong ``readOnlyHint`` on a write then
            gets that write run without approval and retried up to three
            times. Set ``False`` for a server whose hints you do not trust, so
            its tools keep the session default, ``UNCLASSIFIED`` unless
            configured otherwise, unless a ``ToolPolicy`` names them.

    Raises:
        ValueError: If ``name`` is empty or contains other characters, or
            ``command`` is empty.
    """

    name: str
    command: str
    args: Sequence[str] = ()
    env: Mapping[str, str] | None = None
    cwd: str | None = None
    trusts_tool_annotations: bool = True

    def __post_init__(self) -> None:
        _validate_server_name(self.name)
        if not self.command:
            raise ValueError(f"MCP server {self.name!r} needs a command")
        object.__setattr__(self, "args", tuple(self.args))
        if self.env is not None:
            object.__setattr__(self, "env", dict(self.env))


@dataclass(frozen=True, kw_only=True)
class McpStreamableHttpServerConfig:
    """Connect to one MCP server over the Streamable HTTP transport.

    Build it from Worker configuration, never from Flow input, a user message,
    or a browser. Read credentials such as bearer tokens from the Worker's
    environment or secret store and pass them in ``headers``. Every tool call
    opens a new connection, so a server restart between two calls is harmless.

    Example:
        >>> McpStreamableHttpServerConfig(
        ...     name="tickets",
        ...     url="https://tickets.example.com/mcp",
        ...     headers={"Authorization": f"Bearer {os.environ['TICKETS_TOKEN']}"},
        ... )

    Attributes:
        name: Server name, unique within one ``McpToolSource``. It prefixes the
            agent tool names, so it may contain only letters, digits, ``_``,
            and ``-``.
        url: The server's MCP endpoint URL.
        headers: HTTP headers sent with every request, such as
            ``Authorization``. ``None`` sends none.
        trusts_tool_annotations: Whether the server's ``readOnlyHint`` and
            ``idempotentHint`` annotations pick a tool's effect. Defaults to
            ``True``; a wrong ``readOnlyHint`` on a write gets that write
            retried. See ``McpStdioServerConfig.trusts_tool_annotations``.

    Raises:
        ValueError: If ``name`` is empty or contains other characters, or
            ``url`` is not an ``http`` or ``https`` URL.
    """

    name: str
    url: str
    headers: Mapping[str, str] | None = field(default=None, repr=False)
    trusts_tool_annotations: bool = True

    def __post_init__(self) -> None:
        _validate_server_name(self.name)
        if not self.url.startswith(("http://", "https://")):
            raise ValueError(f"MCP server {self.name!r} needs an http or https URL")
        if self.headers is not None:
            object.__setattr__(self, "headers", dict(self.headers))


#: One MCP server configuration: stdio or Streamable HTTP.
McpServerConfig: TypeAlias = McpStdioServerConfig | McpStreamableHttpServerConfig


def _validate_server_name(name: str) -> None:
    if not _SERVER_NAME_PATTERN.fullmatch(name):
        raise ValueError(f"MCP server name {name!r} must be non-empty letters, digits, '_', or '-'")
