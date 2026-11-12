# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The agent CLI commands: how to build each one, its tool names, and its provider settings."""

from __future__ import annotations

import enum
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any

MOCK_OPENAI_API_KEY = "sk-e2e-mock-openai-key"
MOCK_ANTHROPIC_API_KEY = "sk-ant-e2e-mock-anthropic-key"
MOCK_GOOGLE_API_KEY = "e2e-mock-google-key"


class Provider(enum.Enum):
    """The model API a command calls with its default model, and how it is pointed at the mock."""

    OPENAI = "openai"
    ANTHROPIC = "anthropic"
    GEMINI = "gemini"
    PI_ANTHROPIC = "pi-anthropic"

    def environment(self, base_url: str) -> dict[str, str]:
        """Return the provider variables: a dummy key and the mock's base URL."""
        match self:
            case Provider.OPENAI:
                return {"OPENAI_API_KEY": MOCK_OPENAI_API_KEY, "OPENAI_BASE_URL": f"{base_url}/v1"}
            case Provider.ANTHROPIC:
                return {"ANTHROPIC_API_KEY": MOCK_ANTHROPIC_API_KEY, "ANTHROPIC_BASE_URL": base_url}
            case Provider.GEMINI:
                return {"GOOGLE_API_KEY": MOCK_GOOGLE_API_KEY, "GOOGLE_GEMINI_BASE_URL": base_url}
            case Provider.PI_ANTHROPIC:
                return {"ANTHROPIC_API_KEY": MOCK_ANTHROPIC_API_KEY}

    def config_section(self, base_url: str) -> Mapping[str, Any]:
        """Return configuration keys the provider needs; Pi reads its base URL only from there."""
        if self is Provider.PI_ANTHROPIC:
            return {"pi": {"provider_base_urls": {"anthropic": base_url}}}
        return {}


@dataclass(frozen=True)
class ToolDialect:
    """The local tool names and argument names a harness offers its model.

    Attributes:
        read_tool: The file read tool.
        write_tool: The file write tool.
        shell_tool: The shell tool.
        path_argument: The path argument of the file tools.
        has_absolute_paths: Whether the file tools take absolute paths.
        are_local_writes_approved_by_default: Whether file writes and shell
            commands off the allow list wait for approval without
            configuration. When ``False``, the scenario names the write tool
            in ``approval.always`` and expects every shell command to run at once.
    """

    read_tool: str
    write_tool: str
    shell_tool: str
    path_argument: str
    has_absolute_paths: bool
    are_local_writes_approved_by_default: bool = True

    def file_path(self, workspace: Path, name: str) -> str:
        """Return a workspace file's path as the harness's file tools take it."""
        return str(workspace / name) if self.has_absolute_paths else name

    def read_arguments(self, workspace: Path, name: str) -> dict[str, Any]:
        """Return the read tool's arguments."""
        return {self.path_argument: self.file_path(workspace, name)}

    def write_arguments(self, workspace: Path, name: str, content: str) -> dict[str, Any]:
        """Return the write tool's arguments."""
        return {self.path_argument: self.file_path(workspace, name), "content": content}

    def shell_arguments(self, command: str) -> dict[str, Any]:
        """Return the shell tool's arguments."""
        return {"command": command}


CLI_CORE_TOOLS = ToolDialect("read_file", "write_file", "run_shell_command", "path", False)
CLAUDE_CODE_TOOLS = ToolDialect("Read", "Write", "Bash", "file_path", True)
PI_TOOLS = ToolDialect("read", "write", "bash", "path", False)
DEEP_AGENTS_TOOLS = ToolDialect(
    "read_file",
    "write_file",
    "execute",
    "file_path",
    True,
    are_local_writes_approved_by_default=False,
)


@dataclass(frozen=True)
class CommandCase:
    """One agent CLI command.

    Attributes:
        name: The case name, such as ``py-openai-sdk``.
        language: ``python`` or ``typescript``.
        package_dir_name: The package directory under ``agents/cli/<language>``.
        command: The command, such as ``dex-agents-openai-sdk``.
        adapter_dir_name: The TypeScript adapter under ``agents/typescript``, or ``None``.
        tools: The harness's local tool names.
        provider: The model API of the default model.
    """

    name: str
    language: str
    package_dir_name: str
    command: str
    adapter_dir_name: str | None
    tools: ToolDialect
    provider: Provider

    @property
    def is_python(self) -> bool:
        """Report whether the command is a Python command."""
        return self.language == "python"


def _python(name: str, tools: ToolDialect, provider: Provider) -> CommandCase:
    return CommandCase(f"py-{name}", "python", name, f"dex-agents-{name}", None, tools, provider)


def _typescript(
    name: str, adapter: str | None, tools: ToolDialect, provider: Provider
) -> CommandCase:
    return CommandCase(
        f"ts-{name}", "typescript", name, f"dex-agents-{name}", adapter, tools, provider
    )


COMMAND_CASES: tuple[CommandCase, ...] = (
    _python("builtin", CLI_CORE_TOOLS, Provider.OPENAI),
    _python("claude-sdk", CLAUDE_CODE_TOOLS, Provider.ANTHROPIC),
    _python("openai-sdk", CLI_CORE_TOOLS, Provider.OPENAI),
    _python("pydantic-ai", CLI_CORE_TOOLS, Provider.OPENAI),
    _python("deepagents", DEEP_AGENTS_TOOLS, Provider.OPENAI),
    _python("google-adk", CLI_CORE_TOOLS, Provider.GEMINI),
    _typescript("builtin", None, CLI_CORE_TOOLS, Provider.OPENAI),
    _typescript("claude-sdk", "claude-agent-sdk", CLAUDE_CODE_TOOLS, Provider.ANTHROPIC),
    _typescript("openai-sdk", "openai-agents", CLI_CORE_TOOLS, Provider.OPENAI),
    _typescript("vercel-ai", "vercel-ai", CLI_CORE_TOOLS, Provider.OPENAI),
    _typescript("mastra", "mastra", CLI_CORE_TOOLS, Provider.OPENAI),
    _typescript("pi", "pi", PI_TOOLS, Provider.PI_ANTHROPIC),
)
