# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted stand-in for the Claude Code CLI, run by the SDK as ``cli_path``.

It speaks the stream-json subset documented in the README and copies the real
CLI's observed behavior where the adapter depends on it: one assistant message
and transcript entry per content block, PreToolUse callbacks dispatched
concurrently with ``updatedInput`` applied before a decision, defer honored for
every call (so one reply can defer several), a result naming the last deferred
call, ``_meta["claudecode/toolUseId"]`` on every MCP ``tools/call``, and a
``--resume`` of a pending deferred call that continues without a prompt and
records a hidden "Continue from where you left off." message. A hook denial
reaches the model as ``PreToolUse:<tool> hook error: <reason>``. With
``RESUME_SKIPS_HOOKS_ENVIRONMENT_VARIABLE`` set, a resumed call skips the hooks
and asks ``can_use_tool``, as the real CLI can when it starts the call early.

A scripted model reads commands from the last text block of the turn's prompt:

* ``say <text>`` answers ``said: <text>``.
* ``recall`` answers with every earlier prompt of the session.
* ``call <tool> <json>`` calls tools; join calls with `` && `` for one
  reply with several tool uses. A refused call that must run alone is
  called again by itself. ``;; pause <gate>`` waits at a gate before
  answering, and ``;; fail`` fails once every tool call has a result.
* ``stall <gate>`` calls ``lookup``, streams text, and waits at a gate.
* ``fail`` ends the turn with an error result.
"""

from __future__ import annotations

import asyncio
import json
import os
import re
import sys
import uuid
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

CLI_VERSION = "2.1.316"
STATE_DIR_ENVIRONMENT_VARIABLE = "FAKE_CLAUDE_STATE_DIR"
RESUME_SKIPS_HOOKS_ENVIRONMENT_VARIABLE = "FAKE_CLAUDE_RESUME_SKIPS_HOOKS"
INVOCATION_LOG_NAME = "invocations.log"
MODEL_NAME = "claude-scripted"
MODEL_CALL_USAGE = {
    "input_tokens": 10,
    "output_tokens": 5,
    "cache_read_input_tokens": 3,
    "cache_creation_input_tokens": 0,
}
CALL_ALONE_MARKER = "again by itself"
MCP_PROTOCOL_VERSION = "2025-06-18"
CONTINUE_PROMPT = "Continue from where you left off."
INPUT_CLOSE_TIMEOUT_SECONDS = 60.0
HOOK_DECISION_PRIORITY = {"allow": 0, "ask": 1, "defer": 2, "deny": 3}


class ScriptedModelFailure(Exception):
    """The prompt asked the scripted model to fail."""


@dataclass(frozen=True)
class CliArguments:
    resume_session_id: str | None
    mcp_servers: dict[str, Any]
    permission_prompt_tool: str | None
    permission_mode: str
    has_session_mirror: bool


@dataclass(frozen=True)
class ToolRequest:
    request_index: int
    tool_name: str
    tool_input: dict[str, Any]


@dataclass(frozen=True)
class ToolUse:
    tool_use_id: str
    cli_tool_name: str
    tool_input: dict[str, Any]


@dataclass(frozen=True)
class FinalAnswer:
    text: str


@dataclass(frozen=True)
class ToolCalls:
    requests: list[ToolRequest]


@dataclass(frozen=True)
class PauseThenAnswer:
    gate_name: str
    streamed_text: str
    text: str


ModelAction = FinalAnswer | ToolCalls | PauseThenAnswer


def main() -> None:
    arguments = sys.argv[1:]
    if arguments in (["-v"], ["--version"]):
        print(f"{CLI_VERSION} (Claude Code)", flush=True)
        return
    sys.exit(asyncio.run(FakeClaudeCli(parse_cli_arguments(arguments)).run()))


def parse_cli_arguments(arguments: list[str]) -> CliArguments:
    resume_session_id: str | None = None
    mcp_servers: dict[str, Any] = {}
    permission_prompt_tool: str | None = None
    permission_mode = "default"
    has_session_mirror = False
    index = 0
    while index < len(arguments):
        argument = arguments[index]
        if argument.startswith("--resume="):
            resume_session_id = argument.removeprefix("--resume=")
        elif argument == "--mcp-config":
            mcp_servers = json.loads(arguments[index + 1])["mcpServers"]
            index += 1
        elif argument == "--permission-prompt-tool":
            permission_prompt_tool = arguments[index + 1]
            index += 1
        elif argument == "--permission-mode":
            permission_mode = arguments[index + 1]
            index += 1
        elif argument == "--session-mirror":
            has_session_mirror = True
        index += 1
    return CliArguments(
        resume_session_id, mcp_servers, permission_prompt_tool, permission_mode, has_session_mirror
    )


class StreamJsonChannel:
    """Read the SDK's stdin lines and write stream-json lines to stdout."""

    def __init__(self) -> None:
        self._pending_responses: dict[str, asyncio.Future[dict[str, Any]]] = {}
        self._incoming_requests: asyncio.Queue[dict[str, Any]] = asyncio.Queue()
        self._user_messages: asyncio.Queue[dict[str, Any]] = asyncio.Queue()
        self._request_count = 0
        self.input_closed = asyncio.Event()
        self._tasks: list[asyncio.Task[None]] = []

    async def start(self) -> None:
        loop = asyncio.get_running_loop()
        reader = asyncio.StreamReader(limit=1 << 26)
        await loop.connect_read_pipe(lambda: asyncio.StreamReaderProtocol(reader), sys.stdin)
        self._tasks.append(asyncio.create_task(self._read_lines(reader)))

    async def next_incoming_request(self) -> dict[str, Any]:
        return await self._incoming_requests.get()

    async def next_user_message(self) -> dict[str, Any]:
        return await self._user_messages.get()

    def emit(self, message: dict[str, Any]) -> None:
        sys.stdout.write(json.dumps(message) + "\n")
        sys.stdout.flush()

    def respond(self, request_id: str, response: dict[str, Any]) -> None:
        self.emit(
            {
                "type": "control_response",
                "response": {"subtype": "success", "request_id": request_id, "response": response},
            }
        )

    async def request(self, request: dict[str, Any]) -> dict[str, Any]:
        self._request_count += 1
        request_id = f"fake_request_{self._request_count}"
        future: asyncio.Future[dict[str, Any]] = asyncio.get_running_loop().create_future()
        self._pending_responses[request_id] = future
        self.emit({"type": "control_request", "request_id": request_id, "request": request})
        response = await future
        if response.get("subtype") == "error":
            raise RuntimeError(f"SDK answered {request['subtype']} with: {response.get('error')}")
        answer: dict[str, Any] = response.get("response") or {}
        return answer

    async def _read_lines(self, reader: asyncio.StreamReader) -> None:
        while line := await reader.readline():
            message = json.loads(line)
            message_type = message.get("type")
            if message_type == "control_response":
                response = message["response"]
                future = self._pending_responses.pop(response["request_id"], None)
                if future is not None:
                    future.set_result(response)
            elif message_type == "control_request":
                self._incoming_requests.put_nowait(message)
            elif message_type == "user":
                self._user_messages.put_nowait(message)
        self.input_closed.set()


class FakeTranscript:
    """The session JSONL file, mirrored to the SDK line by line."""

    def __init__(
        self,
        path: Path,
        session_id: str,
        entries: list[dict[str, Any]],
        channel: StreamJsonChannel,
        has_session_mirror: bool,
    ) -> None:
        self.path = path
        self.session_id = session_id
        self.entries = entries
        self._channel = channel
        self._has_session_mirror = has_session_mirror

    @classmethod
    def open(cls, arguments: CliArguments, channel: StreamJsonChannel) -> FakeTranscript:
        config_dir = Path(os.environ.get("CLAUDE_CONFIG_DIR") or Path.home() / ".claude")
        projects_dir = config_dir / "projects"
        if arguments.resume_session_id is None:
            session_id = str(uuid.uuid4())
            project_key = re.sub(r"[^a-zA-Z0-9]", "-", os.path.realpath(os.getcwd()))
            path = projects_dir / project_key / f"{session_id}.jsonl"
            path.parent.mkdir(parents=True, exist_ok=True)
            return cls(path, session_id, [], channel, arguments.has_session_mirror)
        session_id = arguments.resume_session_id
        matches = sorted(projects_dir.glob(f"*/{session_id}.jsonl"))
        if not matches:
            raise RuntimeError(f"No conversation found with session ID: {session_id}")
        lines = matches[0].read_text().splitlines()
        entries = [json.loads(line) for line in lines if line.strip()]
        return cls(matches[0], session_id, entries, channel, arguments.has_session_mirror)

    def append(self, fields: dict[str, Any]) -> dict[str, Any]:
        entry = {
            "parentUuid": self._last_uuid(),
            "isSidechain": False,
            "userType": "external",
            "cwd": os.getcwd(),
            "sessionId": self.session_id,
            "version": CLI_VERSION,
            "uuid": str(uuid.uuid4()),
            "timestamp": datetime.now(UTC).isoformat(),
            **fields,
        }
        with self.path.open("a") as transcript_file:
            transcript_file.write(json.dumps(entry) + "\n")
        self.entries.append(entry)
        if self._has_session_mirror:
            self._channel.emit(
                {"type": "transcript_mirror", "filePath": str(self.path), "entries": [entry]}
            )
        return entry

    def pending_deferred_tool_use(self) -> ToolUse | None:
        for index in range(len(self.entries) - 1, -1, -1):
            attachment = self.entries[index].get("attachment")
            if self.entries[index].get("type") != "attachment" or not isinstance(attachment, dict):
                continue
            if attachment.get("type") != "hook_deferred_tool":
                continue
            tool_use_id = attachment["toolUseID"]
            later_results = [
                block
                for entry in self.entries[index + 1 :]
                for block in _content_blocks(entry)
                if block.get("type") == "tool_result" and block.get("tool_use_id") == tool_use_id
            ]
            if later_results:
                return None
            return ToolUse(tool_use_id, attachment["toolName"], attachment["toolInput"])
        return None

    def assistant_entry_with_tool_use(self, tool_use_id: str) -> dict[str, Any]:
        for entry in reversed(self.entries):
            if entry.get("type") == "assistant" and any(
                block.get("type") == "tool_use" and block.get("id") == tool_use_id
                for block in _content_blocks(entry)
            ):
                return entry
        raise RuntimeError(f"tool_use {tool_use_id} not found in transcript")

    def _last_uuid(self) -> str | None:
        for entry in reversed(self.entries):
            entry_uuid = entry.get("uuid")
            if isinstance(entry_uuid, str):
                return entry_uuid
        return None


class ScriptedModel:
    """Decide the next assistant action from the conversation so far."""

    def next_action(self, entries: list[dict[str, Any]]) -> ModelAction:
        prompts = [
            (index, text)
            for index, entry in enumerate(entries)
            if (text := _prompt_text(entry)) is not None
        ]
        if not prompts:
            raise RuntimeError("the conversation has no prompt")
        prompt_index, command = prompts[-1]
        tail = entries[prompt_index + 1 :]
        if command.startswith("say "):
            return FinalAnswer(f"said: {command.removeprefix('say ')}")
        if command == "recall":
            return FinalAnswer("history: " + " | ".join(text for _, text in prompts[:-1]))
        if command == "fail":
            raise ScriptedModelFailure("scripted failure")
        if command.startswith("stall "):
            gate_name = command.removeprefix("stall ")
            lookup = ToolRequest(0, "lookup", {"query": "stall"})
            results = _tool_results_by_request(tail)
            if not results:
                return ToolCalls([lookup])
            return PauseThenAnswer(
                gate_name, f"thinking after {results[0][0]}", f"stalled at {gate_name}"
            )
        if command.startswith("call "):
            script, _, after_tools = command.partition(" ;; ")
            return self._next_tool_action(_parse_tool_requests(script), tail, after_tools)
        raise RuntimeError(f"unknown scripted command {command!r}")

    def _next_tool_action(
        self, requests: list[ToolRequest], tail: list[dict[str, Any]], after_tools: str
    ) -> ModelAction:
        results = _tool_results_by_request(tail)
        if not results:
            return ToolCalls(requests)
        for request in requests:
            result_text, _ = results[request.request_index]
            if CALL_ALONE_MARKER in result_text:
                return ToolCalls([request])
        summary = "; ".join(
            f"{request.tool_name}: {results[request.request_index][0]}" for request in requests
        )
        if after_tools == "fail":
            raise ScriptedModelFailure("scripted failure after tool calls")
        if after_tools.startswith("pause "):
            return PauseThenAnswer(after_tools.removeprefix("pause "), "", summary)
        return FinalAnswer(summary)


class FakeMcpClient:
    """Talk JSON-RPC to the SDK's in-process MCP servers over control requests."""

    def __init__(self, channel: StreamJsonChannel, server_names: list[str]) -> None:
        self._channel = channel
        self._server_names = server_names
        self._tool_names_by_server: dict[str, list[str]] = {}
        self._next_jsonrpc_id = 0

    async def connect(self) -> None:
        for server_name in self._server_names:
            await self._send(
                server_name,
                "initialize",
                {
                    "protocolVersion": MCP_PROTOCOL_VERSION,
                    "capabilities": {},
                    "clientInfo": {"name": "fake-claude-cli", "version": CLI_VERSION},
                },
            )
            await self._channel.request(
                {
                    "subtype": "mcp_message",
                    "server_name": server_name,
                    "message": {"jsonrpc": "2.0", "method": "notifications/initialized"},
                }
            )
            listed = await self._send(server_name, "tools/list", {})
            self._tool_names_by_server[server_name] = [
                listed_tool["name"] for listed_tool in listed["result"]["tools"]
            ]

    def cli_tool_name(self, tool_name: str) -> str:
        for server_name, tool_names in self._tool_names_by_server.items():
            if tool_name in tool_names:
                return f"mcp__{server_name}__{tool_name}"
        return tool_name

    async def call_tool(self, tool_use: ToolUse) -> tuple[str, bool]:
        _, server_name, tool_name = tool_use.cli_tool_name.split("__", 2)
        response = await self._send(
            server_name,
            "tools/call",
            {
                "name": tool_name,
                "arguments": tool_use.tool_input,
                "_meta": {"claudecode/toolUseId": tool_use.tool_use_id},
            },
        )
        if "error" in response:
            return str(response["error"].get("message")), True
        result = response["result"]
        texts = [block["text"] for block in result.get("content", []) if block["type"] == "text"]
        return "\n".join(texts), bool(result.get("isError"))

    async def _send(self, server_name: str, method: str, params: dict[str, Any]) -> dict[str, Any]:
        self._next_jsonrpc_id += 1
        answer = await self._channel.request(
            {
                "subtype": "mcp_message",
                "server_name": server_name,
                "message": {
                    "jsonrpc": "2.0",
                    "id": self._next_jsonrpc_id,
                    "method": method,
                    "params": params,
                },
            }
        )
        mcp_response: dict[str, Any] = answer["mcp_response"]
        return mcp_response


class FakeClaudeCli:
    """Run one SDK connection: handshake, one turn segment, then wait for stdin to close."""

    def __init__(self, arguments: CliArguments) -> None:
        from dex_agents.testing import InvocationLog

        self._arguments = arguments
        self._channel = StreamJsonChannel()
        self._model = ScriptedModel()
        self._state_dir = Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE])
        self._invocations = InvocationLog(self._state_dir / INVOCATION_LOG_NAME)
        self._pre_tool_use_matchers: list[dict[str, Any]] = []
        self._model_call_count = 0
        self._transcript: FakeTranscript | None = None
        self._mcp: FakeMcpClient | None = None

    async def run(self) -> int:
        await self._channel.start()
        initialize = await self._channel.next_incoming_request()
        if initialize["request"]["subtype"] != "initialize":
            raise RuntimeError(f"expected initialize, got {initialize['request']['subtype']}")
        hooks = initialize["request"].get("hooks") or {}
        self._pre_tool_use_matchers = list(hooks.get("PreToolUse") or [])
        self._channel.respond(initialize["request_id"], {"commands": [], "output_style": "default"})
        answering = asyncio.create_task(self._answer_later_control_requests())
        is_resume = self._arguments.resume_session_id is not None
        self._invocations.record("cli:resume" if is_resume else "cli:new")
        self._transcript = FakeTranscript.open(self._arguments, self._channel)
        sdk_servers = [
            name
            for name, config in self._arguments.mcp_servers.items()
            if config.get("type") == "sdk"
        ]
        self._mcp = FakeMcpClient(self._channel, sdk_servers)
        await self._mcp.connect()
        exit_code = 0
        try:
            # Like the real CLI, a resumed deferred call continues the turn without a prompt.
            pending = self._transcript.pending_deferred_tool_use()
            if pending is not None:
                self._invocations.record("cli:auto_resume")
                self._emit_system_init(sdk_servers)
                await self._resume_deferred_tool_use(pending)
            else:
                user_message = await self._channel.next_user_message()
                self._emit_system_init(sdk_servers)
                await self._run_turn_segment(user_message["message"]["content"])
        except ScriptedModelFailure as failure:
            self._emit_result(is_error=True, errors=[str(failure)])
            exit_code = 1
        await self._wait_for_input_close()
        answering.cancel()
        return exit_code

    async def _wait_for_input_close(self) -> None:
        closing = asyncio.create_task(self._channel.input_closed.wait())
        prompting = asyncio.create_task(self._channel.next_user_message())
        done, _ = await asyncio.wait(
            {closing, prompting},
            timeout=INPUT_CLOSE_TIMEOUT_SECONDS,
            return_when=asyncio.FIRST_COMPLETED,
        )
        prompting.cancel()
        closing.cancel()
        if prompting in done:
            self._invocations.record("cli:unexpected_prompt")

    async def _resume_deferred_tool_use(self, pending: ToolUse) -> None:
        transcript = self._require_transcript()
        assistant_entry = transcript.assistant_entry_with_tool_use(pending.tool_use_id)
        if os.environ.get(RESUME_SKIPS_HOOKS_ENVIRONMENT_VARIABLE):
            # The real CLI sometimes starts the resumed call before its hooks exist.
            is_permitted, message = await self._ask_permission(pending)
            if is_permitted:
                output, is_error = await self._execute_tool(pending)
                self._append_tool_result(assistant_entry, pending, output, is_error=is_error)
            else:
                self._append_tool_result(assistant_entry, pending, message, is_error=True)
        elif await self._run_tool_use(assistant_entry, pending) is not None:
            self._emit_deferred_result(pending)
            return
        transcript.append(
            {
                "type": "user",
                "isMeta": True,
                "message": {"role": "user", "content": CONTINUE_PROMPT},
            }
        )
        await self._run_model_loop()

    async def _run_turn_segment(self, prompt_content: Any) -> None:
        self._require_transcript().append(
            {"type": "user", "message": {"role": "user", "content": prompt_content}}
        )
        await self._run_model_loop()

    async def _run_model_loop(self) -> None:
        transcript = self._require_transcript()
        while True:
            action = self._model.next_action(transcript.entries)
            if isinstance(action, FinalAnswer):
                self._answer(action.text)
                return
            if isinstance(action, PauseThenAnswer):
                await self._pause_then_answer(action)
                return
            tool_uses = [
                ToolUse(
                    f"toolu_{request.request_index}_{uuid.uuid4().hex[:12]}",
                    self._require_mcp().cli_tool_name(request.tool_name),
                    request.tool_input,
                )
                for request in action.requests
            ]
            assistant_entries = self._emit_assistant_message(
                [
                    {
                        "type": "tool_use",
                        "id": tool_use.tool_use_id,
                        "name": tool_use.cli_tool_name,
                        "input": tool_use.tool_input,
                    }
                    for tool_use in tool_uses
                ],
                stop_reason="tool_use",
            )
            # The CLI keeps running the batch after a deferral; its result names the last one.
            last_deferred: ToolUse | None = None
            for tool_use, assistant_entry in zip(tool_uses, assistant_entries, strict=True):
                deferred = await self._run_tool_use(assistant_entry, tool_use)
                last_deferred = deferred or last_deferred
            if last_deferred is not None:
                self._emit_deferred_result(last_deferred)
                return

    async def _run_tool_use(
        self, assistant_entry: dict[str, Any], tool_use: ToolUse
    ) -> ToolUse | None:
        """Run one tool use the way the CLI does; return it, with hook input, if deferred."""
        decision, reason, tool_input = await self._run_pre_tool_use_hooks(tool_use)
        tool_use = ToolUse(tool_use.tool_use_id, tool_use.cli_tool_name, tool_input)
        # Each assistant message holds one content block, so the CLI's solo check always passes.
        if decision == "defer":
            self._require_transcript().append(
                {
                    "type": "attachment",
                    "attachment": {
                        "type": "hook_deferred_tool",
                        "toolUseID": tool_use.tool_use_id,
                        "toolName": tool_use.cli_tool_name,
                        "toolInput": tool_use.tool_input,
                        "hookName": "settings",
                        "hookEvent": "PreToolUse",
                        "permissionMode": self._arguments.permission_mode,
                    },
                }
            )
            return tool_use
        if decision == "deny":
            denial = f"PreToolUse:{tool_use.cli_tool_name} hook error: {reason or 'denied'}"
            self._append_tool_result(assistant_entry, tool_use, denial, is_error=True)
            return None
        if decision != "allow":
            is_permitted, message = await self._ask_permission(tool_use)
            if not is_permitted:
                self._append_tool_result(assistant_entry, tool_use, message, is_error=True)
                return None
        output, is_error = await self._execute_tool(tool_use)
        self._append_tool_result(assistant_entry, tool_use, output, is_error=is_error)
        return None

    async def _run_pre_tool_use_hooks(
        self, tool_use: ToolUse
    ) -> tuple[str | None, str | None, dict[str, Any]]:
        """Dispatch every matching callback concurrently, as the CLI does, and merge them."""
        transcript = self._require_transcript()
        hook_input = {
            "session_id": transcript.session_id,
            "transcript_path": str(transcript.path),
            "cwd": os.getcwd(),
            "permission_mode": self._arguments.permission_mode,
            "hook_event_name": "PreToolUse",
            "tool_name": tool_use.cli_tool_name,
            "tool_input": tool_use.tool_input,
            "tool_use_id": tool_use.tool_use_id,
        }
        callback_ids = [
            callback_id
            for matcher in self._pre_tool_use_matchers
            if str(matcher.get("matcher") or "*") == "*"
            or re.fullmatch(str(matcher["matcher"]), tool_use.cli_tool_name)
            for callback_id in matcher.get("hookCallbackIds", [])
        ]
        outputs = await asyncio.gather(
            *(
                self._channel.request(
                    {
                        "subtype": "hook_callback",
                        "callback_id": callback_id,
                        "input": hook_input,
                        "tool_use_id": tool_use.tool_use_id,
                    }
                )
                for callback_id in callback_ids
            )
        )
        decision: str | None = None
        reason: str | None = None
        tool_input = tool_use.tool_input
        for output in outputs:
            specific = output.get("hookSpecificOutput") or {}
            if isinstance(specific.get("updatedInput"), dict):
                tool_input = specific["updatedInput"]
            hook_decision = specific.get("permissionDecision")
            if hook_decision is not None and (
                decision is None
                or HOOK_DECISION_PRIORITY[hook_decision] > HOOK_DECISION_PRIORITY[decision]
            ):
                decision = hook_decision
                reason = specific.get("permissionDecisionReason")
        return decision, reason, tool_input

    async def _ask_permission(self, tool_use: ToolUse) -> tuple[bool, str]:
        if self._arguments.permission_mode == "bypassPermissions":
            return True, ""
        if self._arguments.permission_prompt_tool != "stdio":
            return False, f"Claude requested permissions to use {tool_use.cli_tool_name}."
        answer = await self._channel.request(
            {
                "subtype": "can_use_tool",
                "tool_name": tool_use.cli_tool_name,
                "input": tool_use.tool_input,
                "permission_suggestions": None,
                "blocked_path": None,
                "tool_use_id": tool_use.tool_use_id,
            }
        )
        if answer.get("behavior") == "allow":
            return True, ""
        return False, str(answer.get("message", ""))

    async def _execute_tool(self, tool_use: ToolUse) -> tuple[str, bool]:
        if tool_use.cli_tool_name.startswith("mcp__"):
            return await self._require_mcp().call_tool(tool_use)
        if tool_use.cli_tool_name == "Bash":
            self._invocations.record("Bash")
            return f"ran {tool_use.tool_input['command']}", False
        return f"No such tool available: {tool_use.cli_tool_name}", True

    async def _pause_then_answer(self, action: PauseThenAnswer) -> None:
        from dex_agents.testing import FileGate

        if action.streamed_text:
            self._emit_text_delta(action.streamed_text)
        gate = FileGate(self._state_dir, action.gate_name)
        gate.mark_reached()
        await gate.wait_for_release()
        self._answer(action.text)

    def _answer(self, text: str) -> None:
        half = len(text) // 2
        for chunk in (text[:half], text[half:]):
            if chunk:
                self._emit_text_delta(chunk)
        self._emit_assistant_message([{"type": "text", "text": text}], stop_reason="end_turn")
        self._emit_result(is_error=False, result_text=text)

    def _emit_assistant_message(
        self, content: list[dict[str, Any]], stop_reason: str
    ) -> list[dict[str, Any]]:
        """Write one transcript entry and one SDK message per content block, as the CLI does."""
        self._model_call_count += 1
        self._invocations.record("model_call")
        transcript = self._require_transcript()
        message_id = f"msg_{uuid.uuid4().hex[:16]}"
        request_id = f"req_{uuid.uuid4().hex[:16]}"
        entries = []
        for block in content:
            entry = transcript.append(
                {
                    "type": "assistant",
                    "requestId": request_id,
                    "message": {
                        "id": message_id,
                        "type": "message",
                        "role": "assistant",
                        "model": MODEL_NAME,
                        "content": [block],
                        "stop_reason": stop_reason,
                        "stop_sequence": None,
                        "usage": MODEL_CALL_USAGE,
                    },
                }
            )
            self._channel.emit(
                {
                    "type": "assistant",
                    "message": entry["message"],
                    "parent_tool_use_id": None,
                    "session_id": transcript.session_id,
                    "uuid": entry["uuid"],
                }
            )
            entries.append(entry)
        return entries

    def _append_tool_result(
        self, assistant_entry: dict[str, Any], tool_use: ToolUse, output: str, is_error: bool
    ) -> None:
        transcript = self._require_transcript()
        message = {
            "role": "user",
            "content": [
                {
                    "type": "tool_result",
                    "tool_use_id": tool_use.tool_use_id,
                    "content": output,
                    "is_error": is_error,
                }
            ],
        }
        entry = transcript.append(
            {
                "type": "user",
                "message": message,
                "toolUseResult": output,
                "sourceToolAssistantUUID": assistant_entry["uuid"],
            }
        )
        self._channel.emit(
            {
                "type": "user",
                "message": message,
                "parent_tool_use_id": None,
                "session_id": transcript.session_id,
                "uuid": entry["uuid"],
                "tool_use_result": output,
            }
        )

    def _emit_text_delta(self, text: str) -> None:
        self._channel.emit(
            {
                "type": "stream_event",
                "uuid": str(uuid.uuid4()),
                "session_id": self._require_transcript().session_id,
                "event": {
                    "type": "content_block_delta",
                    "index": 0,
                    "delta": {"type": "text_delta", "text": text},
                },
                "parent_tool_use_id": None,
            }
        )

    def _emit_system_init(self, sdk_servers: list[str]) -> None:
        self._channel.emit(
            {
                "type": "system",
                "subtype": "init",
                "uuid": str(uuid.uuid4()),
                "session_id": self._require_transcript().session_id,
                "apiKeySource": "none",
                "claude_code_version": CLI_VERSION,
                "cwd": os.getcwd(),
                "tools": ["Bash"],
                "mcp_servers": [{"name": name, "status": "connected"} for name in sdk_servers],
                "model": MODEL_NAME,
                "permissionMode": self._arguments.permission_mode,
                "slash_commands": [],
                "output_style": "default",
                "skills": [],
                "plugins": [],
            }
        )

    def _emit_deferred_result(self, tool_use: ToolUse) -> None:
        self._emit_result(
            is_error=False,
            stop_reason="tool_deferred",
            deferred_tool_use={
                "id": tool_use.tool_use_id,
                "name": tool_use.cli_tool_name,
                "input": tool_use.tool_input,
            },
        )

    def _emit_result(
        self,
        *,
        is_error: bool,
        result_text: str = "",
        stop_reason: str = "end_turn",
        deferred_tool_use: dict[str, Any] | None = None,
        errors: list[str] | None = None,
    ) -> None:
        usage = {key: value * self._model_call_count for key, value in MODEL_CALL_USAGE.items()}
        result: dict[str, Any] = {
            "type": "result",
            "subtype": "error_during_execution" if is_error else "success",
            "duration_ms": 1,
            "duration_api_ms": 1,
            "is_error": is_error,
            "num_turns": self._model_call_count,
            "session_id": self._require_transcript().session_id,
            "result": result_text,
            "stop_reason": stop_reason,
            "total_cost_usd": 0.0,
            "usage": usage,
            "uuid": str(uuid.uuid4()),
        }
        if deferred_tool_use is not None:
            result["terminal_reason"] = "tool_deferred"
            result["deferred_tool_use"] = deferred_tool_use
        if errors is not None:
            result["errors"] = errors
        self._channel.emit(result)

    async def _answer_later_control_requests(self) -> None:
        while True:
            request = await self._channel.next_incoming_request()
            self._channel.respond(request["request_id"], {})

    def _require_transcript(self) -> FakeTranscript:
        if self._transcript is None:
            raise RuntimeError("the transcript is not open yet")
        return self._transcript

    def _require_mcp(self) -> FakeMcpClient:
        if self._mcp is None:
            raise RuntimeError("the MCP servers are not connected yet")
        return self._mcp


def _parse_tool_requests(script: str) -> list[ToolRequest]:
    requests: list[ToolRequest] = []
    for request_index, call in enumerate(script.split(" && ")):
        _, tool_name, tool_input = call.split(" ", 2)
        requests.append(ToolRequest(request_index, tool_name, json.loads(tool_input)))
    return requests


def _tool_results_by_request(tail: list[dict[str, Any]]) -> dict[int, tuple[str, bool]]:
    """Return the latest result of each scripted request, keyed by request index."""
    results_by_tool_use_id = {
        block["tool_use_id"]: (str(block.get("content", "")), bool(block.get("is_error")))
        for entry in tail
        for block in _content_blocks(entry)
        if block.get("type") == "tool_result"
    }
    latest: dict[int, tuple[str, bool]] = {}
    for entry in tail:
        for block in _content_blocks(entry):
            if block.get("type") == "tool_use" and block["id"] in results_by_tool_use_id:
                request_index = int(block["id"].split("_")[1])
                latest[request_index] = results_by_tool_use_id[block["id"]]
    return latest


def _prompt_text(entry: dict[str, Any]) -> str | None:
    if entry.get("type") != "user" or entry.get("isMeta"):
        return None
    content = entry.get("message", {}).get("content")
    if isinstance(content, str):
        return content
    if (
        isinstance(content, list)
        and content
        and all(isinstance(block, dict) and block.get("type") == "text" for block in content)
    ):
        last_block_text: str = content[-1]["text"]
        return last_block_text
    return None


def _content_blocks(entry: dict[str, Any]) -> list[dict[str, Any]]:
    message = entry.get("message")
    if not isinstance(message, dict):
        return []
    content = message.get("content")
    if not isinstance(content, list):
        return []
    return [block for block in content if isinstance(block, dict)]


if __name__ == "__main__":
    main()
