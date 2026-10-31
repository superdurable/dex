# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The ``AgentFrameworkRunner`` that runs Claude Agent SDK turns inside Dex Steps."""

from __future__ import annotations

import json
import re
from collections.abc import AsyncIterator, Callable, Mapping, Sequence
from contextvars import ContextVar
from dataclasses import dataclass, replace
from typing import Any, cast

from claude_agent_sdk import (
    AssistantMessage,
    ClaudeAgentOptions,
    ClaudeSDKClient,
    HookMatcher,
    Message,
    MirrorErrorMessage,
    ResultMessage,
    SdkMcpTool,
    StreamEvent,
    ToolResultBlock,
    UserMessage,
)
from claude_agent_sdk.types import HookEvent

from dex_agents import (
    AgentFrameworkRunner,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    DeferredToolExecution,
    FrameworkMessage,
    MessageRole,
    ToolCallsRequested,
    ToolExecutionError,
    ToolExecutionResult,
    TurnCompleted,
)
from dex_agents_claude_agent_sdk.custom_tool_server import build_custom_tool_server
from dex_agents_claude_agent_sdk.session_store import TurnSegmentSessionStore
from dex_agents_claude_agent_sdk.tool_gate import ToolCallGate, ToolCatalog
from dex_agents_claude_agent_sdk.transcript import (
    TranscriptEntry,
    build_turn_prompt_content,
    decode_run_state,
    encode_run_state,
    encode_user_prompt_item,
    load_session_transcript,
    project_turn_entries,
    tool_result_text,
)

#: Name of the in-process MCP server that serves custom tools unless you pass another.
DEFAULT_CUSTOM_TOOL_SERVER_NAME = "tools"

#: Output recorded for an approved tool that runs inside the Claude Code CLI.
#: The model never sees it; the CLI runs the tool when the turn resumes.
APPROVED_CLI_TOOL_OUTPUT = "approved; Claude Code runs this tool when the turn resumes"

_PERMISSION_MODES_THAT_SKIP_THE_ADAPTER = ("acceptEdits", "auto", "bypassPermissions")
AWAIT_INITIALIZE_CLI_FLAG = "await-initialize"
_ADAPTER_OWNED_CLI_FLAGS = (AWAIT_INITIALIZE_CLI_FLAG, "mcp-config", "resume", "session-id")
_SERVER_NAME_PATTERN = re.compile(r"[A-Za-z0-9-]+(?:_[A-Za-z0-9-]+)*")

_running_deferred_tool_execution: ContextVar[DeferredToolExecution | None] = ContextVar(
    "dex_agents_claude_agent_sdk_deferred_tool_execution", default=None
)


@dataclass(frozen=True, kw_only=True)
class ClaudeAgentOptionsContext:
    """Identify the turn segment that asks the options factory for ``ClaudeAgentOptions``.

    ``ClaudeAgentRunner`` calls your options factory once per ``RunModel``
    attempt with this context. Use it to give each session its own working
    directory or sandbox, for example ``cwd=f"/srv/agents/{context.flow_id}"``.
    Keep the result deterministic for a Flow ID: a resumed turn must see the
    same working directory, tools, and model as the attempt that deferred a call.

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        turn_id: ``"turn-<n>"`` of the running turn.
        attempt: One-based ``RunModel`` attempt.
    """

    flow_id: str
    turn_id: str
    attempt: int


#: Builds the application's ``ClaudeAgentOptions`` for one turn segment.
ClaudeAgentOptionsFactory = Callable[[ClaudeAgentOptionsContext], ClaudeAgentOptions]


def current_deferred_tool_execution() -> DeferredToolExecution | None:
    """Return the deferred tool call that the running custom tool handler serves.

    A custom tool handler receives only its arguments, so it cannot see the
    Dex Step that runs it. Call this function inside the handler to read the
    approved record, the attempt, and the session's idempotency key
    ``"<flow_id>/<turn_id>/<tool_call_id>"``, which is the same on every
    attempt. Pass that key to an external system that supports idempotency, so
    an ``IDEMPOTENT_WRITE`` retry has no further effect.

    Example:
        >>> @tool("create_refund", "Refund an order", {"order_id": str})
        ... async def create_refund(arguments):
        ...     execution = current_deferred_tool_execution()
        ...     key = execution.idempotency_key if execution else None
        ...     refund = await payments.refund(arguments["order_id"], idempotency_key=key)
        ...     return {"content": [{"type": "text", "text": f"refunded {refund.id}"}]}

    Returns:
        The ``DeferredToolExecution`` while ``execute_deferred_tool_call`` runs
        the handler in a tool Step, or ``None`` when the handler runs inline
        inside ``RunModel``, as custom READ tools do.
    """
    return _running_deferred_tool_execution.get()


class ClaudeAgentRunner(AgentFrameworkRunner):
    """Run a Claude Agent SDK agent as a durable Dex agent session.

    The Claude Agent SDK runs its agent loop in a Claude Code CLI subprocess
    that cannot stop after one model call, so the runner is a whole-turn
    harness: one ``RunModel`` Step runs a turn segment, from the prompt or a
    deferred tool call to the next deferred call or the final answer. Every
    attempt starts a new CLI process with ``ClaudeSDKClient``. The Dex session
    stores the CLI's transcript entries, and the runner resumes the Claude
    session from them through a ``SessionStore``, so any Worker can run the
    next segment.

    Custom tools, defined with the SDK's ``@tool`` decorator, run in an
    in-process MCP server that the runner creates. A PreToolUse hook applies
    the session's tool policies to every call. Custom READ calls without
    approval run inline, because each deferral restarts the CLI and costs one
    more model call. Every other custom tool call is deferred: the CLI stops
    the segment, the session runs the call once in its own tool Step, possibly
    after approval, and the resumed CLI receives the recorded result instead
    of running the tool again. Only one call per segment leaves the turn;
    later deferrable calls in the same segment are refused and the model
    calls them again. Tools that run inside the CLI, such as ``Bash``, ``Write``, or
    ``Edit``, cannot be recorded. They defer only when their policy requires
    approval, run inside the resumed turn after approval, and are
    at-least-once: a crash during ``RunModel`` can run them again.

    Most applications use ``claude_agent_session_flow`` instead of
    constructing the runner directly.

    Example:
        >>> runner = ClaudeAgentRunner(
        ...     lambda context: ClaudeAgentOptions(
        ...         model="claude-sonnet-5", cwd=f"/srv/agents/{context.flow_id}",
        ...         setting_sources=[]),
        ...     [lookup_order, create_refund],
        ... )
        >>> flow = AgentSessionFlow(runner, flow_type="SupportAgent")

    The runner sets ``mcp_servers[custom_tool_server_name]``, a PreToolUse
    hook, ``can_use_tool``, ``session_store``, ``resume``,
    ``include_partial_messages``, and the ``--await-initialize`` CLI flag in
    ``extra_args`` on the options your factory returns. A turn
    attempt fails with ``ValueError`` when the factory sets ``can_use_tool``,
    ``permission_prompt_tool_name``, ``session_store``, ``resume``,
    ``session_id``, ``continue_conversation``, ``fork_session``,
    ``resume_session_at``, ``resume_drops_turn``, or
    ``enable_file_checkpointing``, or puts ``await-initialize``,
    ``mcp-config``, ``resume``, or ``session-id`` in ``extra_args``; when ``permission_mode`` is
    ``"bypassPermissions"``, ``"acceptEdits"``, or ``"auto"``; when
    ``mcp_servers`` is not a dict or already uses the custom tool server name;
    or when an ``allowed_tools`` entry names a tool whose calls must defer.
    """

    def __init__(
        self,
        options_factory: ClaudeAgentOptionsFactory,
        custom_tools: Sequence[SdkMcpTool[Any]] = (),
        *,
        custom_tool_server_name: str = DEFAULT_CUSTOM_TOOL_SERVER_NAME,
    ) -> None:
        """Create a runner for one agent.

        Args:
            options_factory: Returns the application's ``ClaudeAgentOptions``
                for one turn segment, such as the model, system prompt,
                ``cwd``, built-in ``tools``, ``disallowed_tools``, external
                ``mcp_servers``, and ``env``. It runs on the Worker for every
                ``RunModel`` attempt.
            custom_tools: Tools created with ``claude_agent_sdk.tool``. Their
                MCP annotations classify them when no explicit policy exists:
                ``readOnlyHint`` means ``READ`` and ``idempotentHint`` means
                ``IDEMPOTENT_WRITE``. Policies use the tool's own name, such as
                ``"create_refund"``, not ``"mcp__tools__create_refund"``.
            custom_tool_server_name: Name of the in-process MCP server. The
                model sees custom tools as ``mcp__<name>__<tool>``. Defaults to
                ``"tools"``.

        Raises:
            ValueError: If the server name is not letters, digits, ``-``, and
                single underscores, two custom tools share a name, or a custom
                tool uses a name in ``CLAUDE_CODE_BUILT_IN_TOOL_NAMES``.
        """
        if not _SERVER_NAME_PATTERN.fullmatch(custom_tool_server_name):
            raise ValueError(
                "custom_tool_server_name must be letters, digits, '-', and single underscores"
            )
        self._options_factory = options_factory
        self._tool_catalog = ToolCatalog(list(custom_tools), custom_tool_server_name)

    def build_user_message(self, text: str) -> FrameworkMessage:
        """Store user text, or a turn-failure note, as a prompt for the CLI.

        The session calls it for every user message and for the note it stores
        after a failed turn that ran tools. The next turn sends every such text
        the CLI has not seen yet, oldest first, as its prompt. The CLI then
        records its own copy in the transcript, so a text reaches it only once.

        Args:
            text: The user message text.

        Returns:
            A ``MessageRole.USER`` message whose ``framework_item`` is
            ``{"kind": "user_prompt", "text": text}``.
        """
        return FrameworkMessage(
            role=MessageRole.USER, text=text, framework_item=encode_user_prompt_item(text)
        )

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        """Run or resume one turn segment in a new Claude Code CLI process.

        The Claude Agent SDK is a whole-turn harness: the CLI makes every model
        call of the segment and runs inline tools itself, so one ``RunModel``
        Step covers the segment until the CLI defers a tool call or answers.

        A first segment resumes the Claude session from the transcript of
        earlier turns and sends the turn's prompt. When failed turns left
        prompts or turn-failure notes that no transcript contains, the prompt is
        one text block per stored text, oldest first, ending with this turn's
        prompt. A resumed segment sends no prompt: the CLI finds the pending
        deferred call in the transcript, runs PreToolUse for it again, which now
        answers from the final record, and continues with a hidden "Continue
        from where you left off." message.

        Args:
            turn: Committed session state for this segment.

        Returns:
            ``ToolCallsRequested`` with the one call the CLI deferred, and the
            session ID and the turn's transcript entries so far as run state;
            otherwise ``TurnCompleted`` with the turn's transcript entries
            grouped into messages.

        Raises:
            ValueError: If the options factory returned unsupported options.
            RuntimeError: If the CLI reported an error result, ended without a
                result, deferred a call the adapter did not defer, called a
                Claude Code tool that shares a custom tool's name, or its
                transcript could not be stored.
        """
        committed_transcript = load_session_transcript(turn.messages)
        prompt_content: str | list[dict[str, str]] | None
        if turn.framework_run_state is None:
            session_id = committed_transcript.session_id
            earlier_turn_entries: list[TranscriptEntry] = []
            prompt_content = build_turn_prompt_content(turn.messages, turn.turn_id)
        else:
            suspended_turn = decode_run_state(turn.framework_run_state)
            session_id = suspended_turn.session_id
            earlier_turn_entries = suspended_turn.turn_entries
            prompt_content = None
        session_store = TurnSegmentSessionStore(
            session_id, [*committed_transcript.entries, *earlier_turn_entries]
        )
        tool_call_gate = ToolCallGate(turn, self._tool_catalog)
        options = self._build_turn_options(turn, session_id, session_store, tool_call_gate)
        observer = _TurnSegmentObserver(turn, tool_call_gate)
        # The client keeps stdin open without a prompt, so the CLI can resume a deferred call.
        client = ClaudeSDKClient(options)
        try:
            await client.connect()
            if prompt_content is not None:
                await client.query(_single_user_message(prompt_content))
            async for message in client.receive_response():
                observer.observe(message)
        finally:
            await client.disconnect()
        result = observer.successful_result()
        if tool_call_gate.built_in_tool_name_collisions:
            raise RuntimeError(
                "Claude Code tools share names with custom tools: "
                + ", ".join(sorted(tool_call_gate.built_in_tool_name_collisions))
            )
        segment_session_id, segment_entries = session_store.take_segment_transcript()
        turn_entries = [*earlier_turn_entries, *segment_entries]
        usage = observer.usage(result)
        if result.deferred_tool_use is not None:
            return ToolCallsRequested(
                framework_run_state=encode_run_state(segment_session_id, turn_entries),
                tool_calls=[tool_call_gate.deferred_tool_call(result.deferred_tool_use)],
                usage=usage,
            )
        return TurnCompleted(
            new_messages=project_turn_entries(
                segment_session_id, turn_entries, self._tool_catalog.policy_tool_name
            ),
            usage=usage,
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        """Run one deferred call outside the CLI, in its own tool Step.

        A custom tool's arguments are validated against its input schema, and
        then its handler runs once; ``current_deferred_tool_execution`` returns
        ``call`` inside it. A tool that runs inside the CLI is only marked
        approved here; the resumed turn runs it.

        Args:
            call: The approved record and its idempotency key.

        Returns:
            The custom tool's text output, or ``APPROVED_CLI_TOOL_OUTPUT`` for a
            tool that runs inside the CLI. Non-text content is replaced by a
            short placeholder.

        Raises:
            ToolExecutionError: The arguments do not match the tool's input
                schema, or the custom tool returned ``is_error``. The model sees
                the text.
            Exception: Any exception the handler raises fails the Step attempt.
                An ``IDEMPOTENT_WRITE`` call retries; once its attempts run out,
                or at once for a single-attempt call, the session records
                ``OUTCOME_UNKNOWN``.
        """
        record = call.tool_call
        custom_tool = self._tool_catalog.custom_tool(record.tool_name)
        if custom_tool is None:
            return ToolExecutionResult(output=APPROVED_CLI_TOOL_OUTPUT)
        arguments = json.loads(record.arguments)
        validation_error = self._tool_catalog.validate_custom_tool_input(
            record.tool_name, arguments
        )
        if validation_error is not None:
            raise ToolExecutionError(validation_error)
        token = _running_deferred_tool_execution.set(call)
        try:
            result = await custom_tool.handler(arguments)
        finally:
            _running_deferred_tool_execution.reset(token)
        text = tool_result_text(result.get("content", []))
        if result.get("is_error"):
            raise ToolExecutionError(text)
        return ToolExecutionResult(output=text)

    def _build_turn_options(
        self,
        turn: AgentTurnContext,
        session_id: str | None,
        session_store: TurnSegmentSessionStore,
        tool_call_gate: ToolCallGate,
    ) -> ClaudeAgentOptions:
        options = self._options_factory(
            ClaudeAgentOptionsContext(
                flow_id=turn.flow_id, turn_id=turn.turn_id, attempt=turn.attempt
            )
        )
        self._validate_application_options(options, tool_call_gate)
        server_name = self._tool_catalog.custom_tool_server_name
        mcp_servers = dict(cast(Mapping[str, Any], options.mcp_servers))
        if self._tool_catalog.custom_tools:
            mcp_servers[server_name] = build_custom_tool_server(self._tool_catalog, tool_call_gate)
        hooks: dict[HookEvent, list[HookMatcher]] = {
            event: list(matchers) for event, matchers in (options.hooks or {}).items()
        }
        hooks.setdefault("PreToolUse", []).append(
            HookMatcher(matcher=None, hooks=[tool_call_gate.decide_pre_tool_use])
        )
        return replace(
            options,
            mcp_servers=mcp_servers,
            hooks=hooks,
            can_use_tool=tool_call_gate.decide_tool_permission,
            session_store=session_store,
            resume=session_id,
            include_partial_messages=True,
            # The CLI waits for the hooks before it resumes a deferred call.
            extra_args={**options.extra_args, AWAIT_INITIALIZE_CLI_FLAG: None},
        )

    def _validate_application_options(
        self, options: ClaudeAgentOptions, tool_call_gate: ToolCallGate
    ) -> None:
        adapter_owned_options = {
            "can_use_tool": options.can_use_tool is not None,
            "permission_prompt_tool_name": options.permission_prompt_tool_name is not None,
            "session_store": options.session_store is not None,
            "resume": options.resume is not None,
            "session_id": options.session_id is not None,
            "continue_conversation": options.continue_conversation,
            "fork_session": options.fork_session,
            "resume_session_at": options.resume_session_at is not None,
            "resume_drops_turn": options.resume_drops_turn is not None,
            "enable_file_checkpointing": options.enable_file_checkpointing,
        }
        set_options = [name for name, is_set in adapter_owned_options.items() if is_set]
        set_options += [
            f"extra_args[{flag!r}]"
            for flag in options.extra_args
            if flag in _ADAPTER_OWNED_CLI_FLAGS
        ]
        if set_options:
            raise ValueError(
                f"the Claude agent adapter owns {', '.join(set_options)}; "
                "leave them unset in ClaudeAgentOptions"
            )
        if options.permission_mode in _PERMISSION_MODES_THAT_SKIP_THE_ADAPTER:
            raise ValueError(
                f"permission_mode {options.permission_mode!r} lets tools run without the "
                "session's tool policies; use 'default' or 'dontAsk'"
            )
        if not isinstance(options.mcp_servers, Mapping):
            raise ValueError("pass ClaudeAgentOptions.mcp_servers as a dict of server configs")
        server_name = self._tool_catalog.custom_tool_server_name
        if self._tool_catalog.custom_tools and server_name in options.mcp_servers:
            raise ValueError(f"mcp_servers already has a server named {server_name!r}")
        for allowed_tool in options.allowed_tools:
            cli_tool_name = allowed_tool.split("(", 1)[0].strip()
            policy_tool_name = self._tool_catalog.policy_tool_name(cli_tool_name)
            is_custom_tool = self._tool_catalog.custom_tool_name_for(cli_tool_name) is not None
            policy = tool_call_gate.resolve_policy(cli_tool_name)
            if self._tool_catalog.requires_deferral(is_custom_tool, policy):
                raise ValueError(
                    f"allowed_tools entry {allowed_tool!r} would let {policy_tool_name} run "
                    "without its approval or Step; remove it or give the tool a policy "
                    "that runs inline"
                )


class _TurnSegmentObserver:
    def __init__(self, turn: AgentTurnContext, tool_call_gate: ToolCallGate) -> None:
        self._turn = turn
        self._tool_call_gate = tool_call_gate
        self._model_message_ids: set[str] = set()
        self._mirror_errors: list[str] = []
        self._result: ResultMessage | None = None

    def observe(self, message: Message) -> None:
        if isinstance(message, StreamEvent):
            if message.parent_tool_use_id is None:
                self._write_stream_event_delta(message.event)
        elif isinstance(message, AssistantMessage):
            model_message_id = message.message_id or message.uuid
            if model_message_id is not None:
                self._model_message_ids.add(model_message_id)
        elif isinstance(message, UserMessage):
            if isinstance(message.content, list):
                for block in message.content:
                    if isinstance(block, ToolResultBlock):
                        self._tool_call_gate.record_tool_result(
                            block.tool_use_id, bool(block.is_error)
                        )
        elif isinstance(message, MirrorErrorMessage):
            self._mirror_errors.append(message.error)
        elif isinstance(message, ResultMessage):
            self._result = message

    def successful_result(self) -> ResultMessage:
        if self._mirror_errors:
            raise RuntimeError(
                "storing the Claude Code transcript failed: " + "; ".join(self._mirror_errors)
            )
        if self._result is None:
            raise RuntimeError("the Claude Code CLI ended the turn without a result message")
        if self._result.is_error:
            details = "; ".join(self._result.errors or []) or self._result.result
            raise RuntimeError(
                f"the Claude Code CLI reported {self._result.subtype}: {details or 'no details'}"
            )
        return self._result

    def usage(self, result: ResultMessage) -> AgentUsage:
        result_usage = result.usage or {}
        cache_read_tokens = int(result_usage.get("cache_read_input_tokens") or 0)
        cache_creation_tokens = int(result_usage.get("cache_creation_input_tokens") or 0)
        return AgentUsage(
            model_calls=len(self._model_message_ids),
            input_tokens=int(result_usage.get("input_tokens") or 0)
            + cache_read_tokens
            + cache_creation_tokens,
            output_tokens=int(result_usage.get("output_tokens") or 0),
            cached_input_tokens=cache_read_tokens,
        )

    def _write_stream_event_delta(self, event: Mapping[str, Any]) -> None:
        if event.get("type") != "content_block_delta":
            return
        delta = event.get("delta")
        if not isinstance(delta, Mapping):
            return
        if delta.get("type") == "text_delta":
            self._turn.events.write_text_delta(str(delta.get("text", "")))
        elif delta.get("type") == "thinking_delta":
            self._turn.events.write_reasoning_delta(str(delta.get("thinking", "")))


async def _single_user_message(
    content: str | list[dict[str, str]],
) -> AsyncIterator[dict[str, Any]]:
    yield {
        "type": "user",
        "session_id": "",
        "message": {"role": "user", "content": content},
        "parent_tool_use_id": None,
    }
