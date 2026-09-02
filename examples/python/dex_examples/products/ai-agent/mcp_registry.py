# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Trusted MCP server registry used by the AI Agent Worker."""

from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import os
import re
from collections.abc import AsyncIterator, Awaitable, Callable
from contextlib import AsyncExitStack, asynccontextmanager
from dataclasses import dataclass, field
from pathlib import Path
from time import monotonic
from typing import Any

import httpx2
import yaml
from mcp import ClientSession, types
from mcp.client.stdio import StdioServerParameters, stdio_client
from mcp.client.streamable_http import (
    create_mcp_http_client,
    streamable_http_client,
)

from dex_examples.products.ai_agent.models import (
    ToolDefinition,
    ToolExecutionResult,
)

ProgressWriter = Callable[[str], Awaitable[None]]
LOGGER = logging.getLogger(__name__)


@dataclass(frozen=True)
class ToolPolicy:
    read_only: bool | None = None
    timeout_seconds: float = 60
    maximum_attempts: int | None = None
    retry_total_seconds: float = 300


@dataclass(frozen=True)
class MCPServerConfig:
    name: str
    transport: str
    command: str | None = None
    args: list[str] = field(default_factory=list)
    cwd: str | None = None
    env: dict[str, str] = field(default_factory=dict)
    url: str | None = None
    headers: dict[str, str] = field(default_factory=dict)
    trust_read_only_annotations: bool = False
    tools: dict[str, ToolPolicy] = field(default_factory=dict)


@dataclass(frozen=True)
class RegisteredTool:
    server_name: str
    remote_name: str
    definition: ToolDefinition


class MCPRegistry:
    def __init__(self, servers: list[MCPServerConfig]) -> None:
        self._server_configs = {server.name: server for server in servers}
        if len(self._server_configs) != len(servers):
            raise ValueError("MCP server names must be unique")
        self._is_started = False
        self._tools: dict[str, RegisteredTool] = {}

    @classmethod
    def from_file(cls, path: Path | None) -> MCPRegistry:
        if path is None:
            return cls([])
        payload = yaml.safe_load(path.read_text()) or {}
        raw_servers = payload.get("servers", [])
        if not isinstance(raw_servers, list):
            raise ValueError("MCP config servers must be a list")
        return cls([_parse_server(item) for item in raw_servers])

    async def start(self) -> None:
        if self._is_started:
            return
        self._tools.clear()
        for config in self._server_configs.values():
            async with _server_session(config) as session:
                await self._discover_tools(config, session)
        self._is_started = True

    async def close(self) -> None:
        self._is_started = False
        self._tools.clear()

    @property
    def server_names(self) -> list[str]:
        return sorted(self._server_configs)

    @property
    def tool_names(self) -> list[str]:
        return sorted(self._tools)

    @property
    def registered_tools(self) -> list[RegisteredTool]:
        return sorted(
            self._tools.values(),
            key=lambda tool: tool.definition.name,
        )

    def definitions(
        self,
        enabled_servers: list[str],
        enabled_tools: list[str],
    ) -> list[ToolDefinition]:
        servers = set(enabled_servers or self.server_names)
        names = set(enabled_tools)
        definitions = [
            tool.definition
            for tool in self._tools.values()
            if tool.server_name in servers and (not names or tool.definition.name in names)
        ]
        if servers:
            definitions.extend(
                definition
                for definition in _broker_definitions()
                if not names or definition.name in names
            )
        return sorted(definitions, key=lambda definition: definition.name)

    def definition(self, name: str) -> ToolDefinition:
        if not self._is_started:
            raise RuntimeError("MCP registry is not started")
        if name in _BROKER_NAMES:
            return next(item for item in _broker_definitions() if item.name == name)
        try:
            return self._tools[name].definition
        except KeyError as error:
            raise ValueError(f"unknown MCP tool {name!r}") from error

    async def execute(
        self,
        name: str,
        arguments: dict[str, Any],
        enabled_servers: list[str],
        write_progress: ProgressWriter,
    ) -> ToolExecutionResult:
        if name in _BROKER_NAMES:
            return await self._execute_broker(
                name,
                arguments,
                set(enabled_servers or self.server_names),
            )
        try:
            registered = self._tools[name]
        except KeyError as error:
            raise ValueError(f"unknown MCP tool {name!r}") from error
        allowed_servers = set(enabled_servers or self.server_names)
        if registered.server_name not in allowed_servers:
            raise ValueError(f"MCP server {registered.server_name!r} is not enabled")
        return await self._execute_with_retry(registered, arguments, write_progress)

    async def _discover_tools(
        self,
        config: MCPServerConfig,
        session: ClientSession,
    ) -> None:
        cursor: str | None = None
        while True:
            result = await session.list_tools(
                params=types.PaginatedRequestParams(cursor=cursor)
            )
            for tool in result.tools:
                public_name = _component_name(config.name, tool.name)
                if public_name in self._tools:
                    raise ValueError(f"duplicate normalized MCP tool name {public_name!r}")
                policy = config.tools.get(tool.name, ToolPolicy())
                is_read_only = policy.read_only
                if (
                    is_read_only is None
                    and config.trust_read_only_annotations
                    and tool.annotations is not None
                ):
                    is_read_only = tool.annotations.read_only_hint is True
                maximum_attempts = policy.maximum_attempts
                if maximum_attempts is None:
                    maximum_attempts = 3 if is_read_only else 1
                if maximum_attempts <= 0:
                    raise ValueError(f"maximum_attempts for {public_name!r} must be positive")
                if policy.timeout_seconds <= 0 or policy.retry_total_seconds <= 0:
                    raise ValueError(
                        f"timeouts for {public_name!r} must be positive"
                    )
                definition = ToolDefinition(
                    name=public_name,
                    description=tool.description or f"MCP tool {tool.name}",
                    input_schema=tool.input_schema,
                    requires_approval=is_read_only is not True,
                    timeout_seconds=policy.timeout_seconds,
                    maximum_attempts=maximum_attempts,
                    retry_total_seconds=policy.retry_total_seconds,
                )
                self._tools[public_name] = RegisteredTool(
                    config.name,
                    tool.name,
                    definition,
                )
            cursor = result.next_cursor
            if not cursor:
                break

    async def _execute_with_retry(
        self,
        tool: RegisteredTool,
        arguments: dict[str, Any],
        write_progress: ProgressWriter,
    ) -> ToolExecutionResult:
        definition = tool.definition
        deadline = monotonic() + definition.retry_total_seconds
        attempts = 0
        last_error: Exception | None = None
        while attempts < definition.maximum_attempts and monotonic() <= deadline:
            attempts += 1
            try:
                await write_progress(
                    f"Calling {definition.name} (attempt {attempts})."
                )
                config = self._server_configs[tool.server_name]
                async with _server_session(config) as session:
                    async with asyncio.timeout(definition.timeout_seconds):
                        result = await session.call_tool(
                            tool.remote_name,
                            arguments,
                            progress_callback=_ProgressBridge(write_progress),
                            allow_input_required=False,
                        )
                content = json.dumps(
                    result.model_dump(mode="json", by_alias=True),
                    ensure_ascii=False,
                )
                return ToolExecutionResult(
                    content=content,
                    is_error=bool(getattr(result, "is_error", False)),
                )
            except Exception as error:
                last_error = error
                if attempts >= definition.maximum_attempts or monotonic() > deadline:
                    break
                await asyncio.sleep(min(2 ** (attempts - 1), 5))
        assert last_error is not None
        LOGGER.warning(
            "MCP tool %s failed after %s attempts",
            definition.name,
            attempts,
            exc_info=last_error,
        )
        return ToolExecutionResult(
            content=json.dumps(
                {
                    "status": "failed",
                    "attempts": attempts,
                    "outcome": (
                        "known_failure" if definition.requires_approval is False else "outcome_unknown"
                    ),
                    "error_type": type(last_error).__name__,
                },
                ensure_ascii=False,
            ),
            is_error=True,
        )

    async def _execute_broker(
        self,
        name: str,
        arguments: dict[str, Any],
        enabled_servers: set[str],
    ) -> ToolExecutionResult:
        server_name = str(arguments.get("server", ""))
        if server_name not in enabled_servers or server_name not in self._server_configs:
            raise ValueError(f"MCP server {server_name!r} is not enabled")
        async with _server_session(self._server_configs[server_name]) as session:
            if name == "mcp_list_resources":
                result = await session.list_resources()
            elif name == "mcp_read_resource":
                result = await session.read_resource(str(arguments.get("uri", "")))
            elif name == "mcp_list_resource_templates":
                result = await session.list_resource_templates()
            elif name == "mcp_list_prompts":
                result = await session.list_prompts()
            else:
                prompt_arguments = arguments.get("arguments")
                result = await session.get_prompt(
                    str(arguments.get("name", "")),
                    prompt_arguments if isinstance(prompt_arguments, dict) else None,
                    allow_input_required=False,
                )
        return ToolExecutionResult(
            json.dumps(result.model_dump(mode="json", by_alias=True), ensure_ascii=False),
            False,
        )


class _ProgressBridge:
    def __init__(self, write_progress: ProgressWriter) -> None:
        self._write_progress = write_progress

    async def __call__(
        self,
        progress: float,
        total: float | None,
        message: str | None,
    ) -> None:
        detail = message or f"MCP progress: {progress}"
        if total is not None:
            detail = f"{detail} / {total}"
        await self._write_progress(detail)


async def _connect_server(
    stack: AsyncExitStack,
    config: MCPServerConfig,
) -> ClientSession:
    if config.transport == "stdio":
        if not config.command:
            raise ValueError(f"stdio MCP server {config.name!r} requires command")
        child_env = {
            name: value
            for name in ("PATH", "HOME", "TMPDIR")
            if (value := os.environ.get(name)) is not None
        }
        child_env.update(_resolve_env(config.env))
        transport = stdio_client(
            StdioServerParameters(
                command=config.command,
                args=config.args,
                cwd=config.cwd,
                env=child_env,
            )
        )
        read_stream, write_stream = await stack.enter_async_context(transport)
    elif config.transport == "streamable_http":
        if not config.url:
            raise ValueError(f"HTTP MCP server {config.name!r} requires url")
        http_client = create_mcp_http_client(
            headers=_resolve_env(config.headers),
            timeout=httpx2.Timeout(30, read=300),
        )
        await stack.enter_async_context(http_client)
        transport = streamable_http_client(
            config.url,
            http_client=http_client,
        )
        read_stream, write_stream = await stack.enter_async_context(transport)
    else:
        raise ValueError(f"unsupported MCP transport {config.transport!r}")
    session = await stack.enter_async_context(
        ClientSession(
            read_stream,
            write_stream,
            logging_callback=_log_server_message,
        )
    )
    await session.initialize()
    return session


async def _log_server_message(
    params: types.LoggingMessageNotificationParams,
) -> None:
    LOGGER.info("MCP server log: %s", params.model_dump_json(by_alias=True))


@asynccontextmanager
async def _server_session(
    config: MCPServerConfig,
) -> AsyncIterator[ClientSession]:
    stack = AsyncExitStack()
    try:
        yield await _connect_server(stack, config)
    finally:
        await stack.aclose()


def _parse_server(raw: object) -> MCPServerConfig:
    if not isinstance(raw, dict):
        raise ValueError("each MCP server config must be an object")
    raw_tools = raw.get("tools", {})
    if not isinstance(raw_tools, dict):
        raise ValueError("MCP tools policy must be an object")
    tools = {
        str(name): _parse_policy(policy)
        for name, policy in raw_tools.items()
    }
    raw_args = raw.get("args", [])
    if not isinstance(raw_args, list):
        raise ValueError("MCP server args must be a list")
    name = str(raw.get("name", ""))
    if not name:
        raise ValueError("MCP server name must not be empty")
    return MCPServerConfig(
        name=name,
        transport=str(raw.get("transport", "")),
        command=_optional_string(raw.get("command")),
        args=[str(item) for item in raw_args],
        cwd=_optional_string(raw.get("cwd")),
        env=_string_map(raw.get("env", {})),
        url=_optional_string(raw.get("url")),
        headers=_string_map(raw.get("headers", {})),
        trust_read_only_annotations=bool(raw.get("trust_read_only_annotations", False)),
        tools=tools,
    )


def _parse_policy(raw: object) -> ToolPolicy:
    if not isinstance(raw, dict):
        raise ValueError("MCP tool policy must be an object")
    read_only = raw.get("read_only")
    if read_only is not None and not isinstance(read_only, bool):
        raise ValueError("read_only must be a boolean")
    maximum_attempts = raw.get("maximum_attempts")
    return ToolPolicy(
        read_only=read_only,
        timeout_seconds=float(raw.get("timeout_seconds", 60)),
        maximum_attempts=(int(maximum_attempts) if maximum_attempts is not None else None),
        retry_total_seconds=float(raw.get("retry_total_seconds", 300)),
    )


def _resolve_env(mapping: dict[str, str]) -> dict[str, str]:
    resolved: dict[str, str] = {}
    for target_name, source_name in mapping.items():
        value = os.environ.get(source_name)
        if value is None:
            raise ValueError(f"required environment variable {source_name!r} is unset")
        resolved[target_name] = value
    return resolved


def _string_map(raw: object) -> dict[str, str]:
    if not isinstance(raw, dict):
        raise ValueError("expected an object of string values")
    return {str(key): str(value) for key, value in raw.items()}


def _optional_string(value: object) -> str | None:
    return str(value) if value is not None else None


def _component_name(server_name: str, component_name: str) -> str:
    normalized_server = re.sub(r"[^A-Za-z0-9_]", "_", server_name)
    normalized_component = re.sub(r"[^A-Za-z0-9_]", "_", component_name)
    if not normalized_server or not normalized_component:
        raise ValueError("MCP names must contain an alphanumeric character")
    candidate = f"{normalized_server}__{normalized_component}"
    if len(candidate) <= 64:
        return candidate
    digest = hashlib.sha256(candidate.encode()).hexdigest()[:8]
    return f"{candidate[:55]}_{digest}"


_BROKER_NAMES = {
    "mcp_list_resources",
    "mcp_read_resource",
    "mcp_list_resource_templates",
    "mcp_list_prompts",
    "mcp_get_prompt",
}


def _broker_definitions() -> list[ToolDefinition]:
    common = dict(
        requires_approval=False,
        timeout_seconds=60,
        maximum_attempts=3,
        retry_total_seconds=300,
    )
    return [
        ToolDefinition(
            "mcp_list_resources",
            "List resources exposed by an enabled MCP server.",
            {"type": "object", "properties": {"server": {"type": "string"}}, "required": ["server"]},
            **common,
        ),
        ToolDefinition(
            "mcp_read_resource",
            "Read one resource from an enabled MCP server.",
            {
                "type": "object",
                "properties": {"server": {"type": "string"}, "uri": {"type": "string"}},
                "required": ["server", "uri"],
            },
            **common,
        ),
        ToolDefinition(
            "mcp_list_resource_templates",
            "List resource templates exposed by an enabled MCP server.",
            {"type": "object", "properties": {"server": {"type": "string"}}, "required": ["server"]},
            **common,
        ),
        ToolDefinition(
            "mcp_list_prompts",
            "List prompts exposed by an enabled MCP server.",
            {"type": "object", "properties": {"server": {"type": "string"}}, "required": ["server"]},
            **common,
        ),
        ToolDefinition(
            "mcp_get_prompt",
            "Render a prompt from an enabled MCP server as tool data.",
            {
                "type": "object",
                "properties": {
                    "server": {"type": "string"},
                    "name": {"type": "string"},
                    "arguments": {"type": "object"},
                },
                "required": ["server", "name"],
            },
            **common,
        ),
    ]
