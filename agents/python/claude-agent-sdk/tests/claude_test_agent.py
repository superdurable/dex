# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Claude agent under test: custom tools, policies, and options for the fake CLI.

* ``lookup`` is READ through its ``readOnlyHint`` annotation and runs inline.
* ``publish`` is a non-idempotent write that needs approval.
* ``charge`` is an idempotent write through its ``idempotentHint`` annotation.
* ``slow_write`` is a non-idempotent write without approval that waits at a gate.
* ``Bash`` runs inside the CLI and needs approval, the default for unknown tools.
  The Claude Code tool permission runs ``INLINE_BASH_COMMAND`` without
  approval, denies commands that start with ``DENIED_BASH_COMMAND_PREFIX``,
  and raises for ``FAILING_PERMISSION_BASH_COMMAND``.

The fake ledger Connector's operations are source tools: ``get_balance`` is a
Query; ``create_payout`` is a ``retry: safe`` Mutation without approval and
``approved_payout`` the same with the manifest's approval; ``payout_checked``
needs approval above 50; ``post_note`` is a single-attempt Mutation and
``policy_note`` the same under a session policy that asks for approval and
claims ``READ``. ``HidingToolSource`` lists ``hiding_note``, the same
single-attempt Mutation, until a test hides it. ``TaskToolSource`` lists
``report``, a ``READ`` tool without approval that starts a tool task.

An application PreToolUse hook rewrites ``publish`` input ``"rewrite me"`` to
``"rewritten"``, raises a ``payout_checked`` amount to 500 when its memo is
``raise``, rewrites the ``Bash`` command ``ORIGINAL_BASH_COMMAND`` to
``REWRITTEN_BASH_COMMAND``, and appends ``+`` to any ``publish`` message or
``lookup`` query that starts with ``stamp`` on every call it sees; the adapter skips
application hooks for a resumed call that has a final record. Another denies
``publish`` and ``post_note`` while ``DENY_FLAG_FILE_NAME`` exists. With
``REAL_CLI_ENVIRONMENT_VARIABLE`` set, the agent runs the bundled Claude Code
CLI with that JSON object as extra CLI environment. A JSON object in
``EXTRA_CLI_ENVIRONMENT_VARIABLE`` then changes the CLI environment: a string
sets a variable, and ``null`` removes it, such as ``CLAUDE_CONFIG_DIR``. A comma-separated
``ALLOWED_TOOLS_ENVIRONMENT_VARIABLE`` becomes ``allowed_tools``.

With ``MEDIA_STORE_ENVIRONMENT_VARIABLE`` set, the agent takes images from a
``LocalDirectoryMediaStore`` under the state directory and adds two tools:
``screenshot``, a source tool that needs approval, uploads the GIF test image,
and returns its reference; and ``draw``, a deferred custom tool without
approval that returns the JPEG test image as MCP ``image`` content.
"""

from __future__ import annotations

import base64
import dataclasses
import json
import os
from collections.abc import Mapping, Sequence
from datetime import timedelta
from pathlib import Path
from typing import Any, cast

from claude_agent_sdk import (
    ClaudeAgentOptions,
    HookCallback,
    HookContext,
    HookInput,
    HookJSONOutput,
    HookMatcher,
    PermissionMode,
    SdkMcpTool,
    ToolAnnotations,
    tool,
)

from dex_agents import (
    AgentSessionFlow,
    AgentToolCall,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    DeferredToolOutcome,
    LocalDirectoryMediaStore,
    MediaStore,
    ModelCallCheck,
    ModelCallDecision,
    ModelUsageReport,
    RunModelLimit,
    ToolCallStepOptions,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    ToolTaskCancellation,
    ToolTaskPending,
    ToolTaskPoll,
    ToolTaskPollOutcome,
    ToolTaskStarted,
)
from dex_agents.builtin_loop import SourceToolDefinition
from dex_agents.connectors import ConnectorToolSource, connector_tool
from dex_agents.testing import FileGate, InvocationLog
from dex_agents_claude_agent_sdk import (
    ClaudeAgentOptionsContext,
    ClaudeCodeToolDenial,
    claude_agent_session_flow,
    current_deferred_tool_execution,
)
from tests.fake_claude_cli import INVOCATION_LOG_NAME, STATE_DIR_ENVIRONMENT_VARIABLE
from tests.fake_ledger import (
    LEDGER_CONNECTION,
    CreatePayoutOperation,
    FakeLedgerProvider,
    GetBalanceOperation,
    PostNoteOperation,
)

CLAUDE_TEST_AGENT_FLOW_TYPE = "ScriptedClaudeAgent"
FAKE_CLI_PATH_ENVIRONMENT_VARIABLE = "FAKE_CLAUDE_CLI_PATH"
REAL_CLI_ENVIRONMENT_VARIABLE = "CLAUDE_TEST_AGENT_REAL_CLI_ENV"
EXTRA_CLI_ENVIRONMENT_VARIABLE = "CLAUDE_TEST_AGENT_EXTRA_CLI_ENV"
PERMISSION_MODE_ENVIRONMENT_VARIABLE = "CLAUDE_TEST_AGENT_PERMISSION_MODE"
ALLOWED_TOOLS_ENVIRONMENT_VARIABLE = "CLAUDE_TEST_AGENT_ALLOWED_TOOLS"
RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE = "CLAUDE_TEST_AGENT_RUN_MODEL_LIMIT"
SPEND_LIMIT_ENVIRONMENT_VARIABLE = "CLAUDE_TEST_AGENT_SPEND_LIMIT"
SPEND_REFUSAL_FILE_NAME = "spend-refusal"
SPEND_CHECKS_LOG_NAME = "spend-checks.jsonl"
SPEND_REPORTS_LOG_NAME = "spend-reports.jsonl"
REWRITTEN_PUBLISH_INPUT = {"message": "rewritten"}
RAISED_PAYOUT_AMOUNT = 500
BASH_OUTPUT_FILE_NAME = "bash-ran.txt"
INLINE_BASH_COMMAND = "echo inline"
DENIED_BASH_COMMAND_PREFIX = "forbidden"
DENIED_BASH_COMMAND_REASON = "forbidden commands never run"
FAILING_PERMISSION_BASH_COMMAND = "break the permission check"
ORIGINAL_BASH_COMMAND = f"echo original-input | tee {BASH_OUTPUT_FILE_NAME}"
REWRITTEN_BASH_COMMAND = f"echo rewritten-input | tee {BASH_OUTPUT_FILE_NAME}"
HIDE_SOURCE_TOOLS_FILE_NAME = "hide-source-tools"
DENY_FLAG_FILE_NAME = "deny-publish-and-post-note"
APPLICATION_HOOK_DENIAL = "the application hook denies this call"
SLOW_WRITE_GATE = "slow_write"
MEDIA_STORE_ENVIRONMENT_VARIABLE = "CLAUDE_TEST_AGENT_MEDIA_STORE"
MEDIA_DIRECTORY_NAME = "media"
TOOL_POLICIES = {
    "publish": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
    "slow_write": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "policy_note": ToolPolicy(ToolEffect.READ, requires_approval=True),
    "screenshot": ToolPolicy(ToolEffect.READ, requires_approval=True),
    "draw": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "report": ToolPolicy(ToolEffect.READ, requires_approval=False),
}
TEST_IMAGES = {
    image["name"]: image
    for image in json.loads(
        (Path(__file__).resolve().parents[3] / "conformance" / "images.json").read_text()
    )["images"]
}


def conformance_image_bytes(name: str) -> bytes:
    """Return the bytes of one conformance image: ``png``, ``jpeg``, ``gif``, or ``webp``."""
    return base64.b64decode(TEST_IMAGES[name]["base64"])


def build_media_store(state_dir: Path) -> LocalDirectoryMediaStore:
    return LocalDirectoryMediaStore(state_dir / MEDIA_DIRECTORY_NAME)


def build_claude_test_agent_flow(state_dir: Path) -> AgentSessionFlow:
    def build_options(context: ClaudeAgentOptionsContext) -> ClaudeAgentOptions:
        real_cli_environment = os.environ.get(REAL_CLI_ENVIRONMENT_VARIABLE)
        permission_mode = os.environ.get(PERMISSION_MODE_ENVIRONMENT_VARIABLE)
        allowed_tools = os.environ.get(ALLOWED_TOOLS_ENVIRONMENT_VARIABLE, "")
        return ClaudeAgentOptions(
            cli_path=None
            if real_cli_environment is not None
            else os.environ[FAKE_CLI_PATH_ENVIRONMENT_VARIABLE],
            cwd=str(state_dir / "workspace"),
            env=_build_cli_environment(state_dir, real_cli_environment),
            system_prompt=f"You are scripted agent {context.flow_id}.",
            tools=["Bash"],
            allowed_tools=[name for name in allowed_tools.split(",") if name],
            setting_sources=[],
            permission_mode=cast(PermissionMode | None, permission_mode),
            hooks={
                "PreToolUse": [
                    HookMatcher(matcher="mcp__tools__publish", hooks=[rewrite_publish_input]),
                    HookMatcher(matcher="mcp__tools__(publish|lookup)", hooks=[stamp_tool_input]),
                    HookMatcher(matcher="mcp__tools__payout_checked", hooks=[raise_checked_payout]),
                    HookMatcher(matcher="Bash", hooks=[rewrite_bash_command]),
                    HookMatcher(
                        matcher="mcp__tools__(publish|post_note)",
                        hooks=[_deny_while_flagged(state_dir)],
                    ),
                ]
            },
        )

    media_store = (
        build_media_store(state_dir) if os.environ.get(MEDIA_STORE_ENVIRONMENT_VARIABLE) else None
    )
    custom_tools = build_custom_tools(state_dir)
    tool_sources: list[Any] = [
        build_connector_source(state_dir),
        HidingToolSource(state_dir),
        TaskToolSource(state_dir),
    ]
    if media_store is not None:
        custom_tools.append(build_draw_tool(state_dir))
        tool_sources.append(ScreenshotToolSource(state_dir, media_store))
    return claude_agent_session_flow(
        build_options,
        custom_tools,
        flow_type=CLAUDE_TEST_AGENT_FLOW_TYPE,
        tool_policies=TOOL_POLICIES,
        tool_sources=tool_sources,
        claude_code_tool_permission=decide_claude_code_tool_permission,
        run_model_limit=_run_model_limit_from_environment(),
        spend_limit=(
            FileSpendLimit(state_dir) if os.environ.get(SPEND_LIMIT_ENVIRONMENT_VARIABLE) else None
        ),
        media_store=media_store,
    )


def decide_claude_code_tool_permission(
    turn: AgentTurnContext, tool_name: str, tool_input: Mapping[str, Any]
) -> ToolPolicy | ClaudeCodeToolDenial | None:
    command = str(tool_input.get("command", "")) if tool_name == "Bash" else ""
    if command == INLINE_BASH_COMMAND:
        return ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False)
    if command.startswith(DENIED_BASH_COMMAND_PREFIX):
        return ClaudeCodeToolDenial(DENIED_BASH_COMMAND_REASON)
    if command == FAILING_PERMISSION_BASH_COMMAND:
        raise RuntimeError("the permission check broke")
    return None


class _ListedTool:
    def __init__(self, name: str, description: str) -> None:
        self.name = name
        self.description = description
        self.input_schema: dict[str, Any] = {"type": "object", "properties": {}}


class ScreenshotToolSource:
    """Lists ``screenshot``, which uploads the GIF test image and returns its reference."""

    source_name = "screenshots"

    def __init__(self, state_dir: Path, media_store: MediaStore) -> None:
        self._invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)
        self._media_store = media_store

    async def list_tools(self) -> Sequence[SourceToolDefinition]:
        return [_ListedTool("screenshot", "Take a screenshot")]

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition,
    ) -> DeferredToolCall:
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=turn.resolve_tool_policy(tool_name, ToolEffect.READ),
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        self._invocations.record("screenshot")
        reference = await self._media_store.put_image(conformance_image_bytes("gif"))
        return ToolExecutionResult(output="took a screenshot", images=[reference])

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        raise RuntimeError("screenshot starts no tool task")


def build_draw_tool(state_dir: Path) -> SdkMcpTool[Any]:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    @tool("draw", "Draw a picture", {"subject": str})
    async def draw(arguments: dict[str, Any]) -> dict[str, Any]:
        invocations.record("draw")
        return {
            "content": [
                {"type": "text", "text": f"drew {arguments['subject']}"},
                {"type": "image", "data": TEST_IMAGES["jpeg"]["base64"], "mimeType": "image/jpeg"},
            ]
        }

    return draw


def build_connector_source(state_dir: Path) -> ConnectorToolSource:
    provider = FakeLedgerProvider(state_dir)
    payouts = CreatePayoutOperation(provider)
    notes = PostNoteOperation(provider)
    return ConnectorToolSource(
        [
            connector_tool(GetBalanceOperation(provider), LEDGER_CONNECTION, name="get_balance"),
            connector_tool(
                payouts, LEDGER_CONNECTION, name="create_payout", requires_approval=False
            ),
            connector_tool(payouts, LEDGER_CONNECTION, name="approved_payout"),
            connector_tool(
                payouts,
                LEDGER_CONNECTION,
                name="payout_checked",
                requires_approval=lambda arguments: arguments["amount"] > 50,
            ),
            connector_tool(notes, LEDGER_CONNECTION, name="post_note"),
            connector_tool(notes, LEDGER_CONNECTION, name="policy_note"),
        ]
    )


class HidingToolSource:
    """A dynamic source of ``hiding_note`` whose listing a test can empty or break.

    While ``HIDE_SOURCE_TOOLS_FILE_NAME`` exists, ``list_tools`` returns nothing
    when the file says ``empty`` and raises when it says ``raise``.
    """

    source_name = "hiding-ledger"

    def __init__(self, state_dir: Path) -> None:
        self._state_dir = state_dir
        notes = PostNoteOperation(FakeLedgerProvider(state_dir))
        self._inner = ConnectorToolSource(
            [connector_tool(notes, LEDGER_CONNECTION, name="hiding_note")],
            source_name=self.source_name,
        )

    async def list_tools(self) -> Sequence[SourceToolDefinition]:
        hide_file = self._state_dir / HIDE_SOURCE_TOOLS_FILE_NAME
        if hide_file.exists():
            if hide_file.read_text() == "raise":
                raise ConnectionError("the hiding source is down")
            return ()
        return await self._inner.list_tools()

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition,
    ) -> DeferredToolCall:
        return self._inner.deferred_tool_call(
            turn,
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            listed_tool=listed_tool,
        )

    def tool_call_step_options(self, tool_call: AgentToolCall) -> ToolCallStepOptions | None:
        return self._inner.tool_call_step_options(tool_call)

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        return await self._inner.execute_deferred_tool_call(call)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        return await self._inner.poll_tool_task(poll)


class TaskToolSource:
    """Lists ``report``, whose call starts a task that ``poll_tool_task`` polls.

    The call returns ``ToolTaskStarted`` with task ID ``task-<tool call ID>``.
    Arguments: ``topic`` names the result ``report on <topic>``; ``polls`` is
    the poll that finishes the task, counted from 1; ``fail`` makes that poll
    raise ``ToolExecutionError``; ``hold`` keeps the task pending. The
    invocation log records ``start <task>``, ``poll <task> <n>``, and
    ``cancel-task <task> <status> <cancellation_reason>``.
    """

    source_name = "reports"

    def __init__(self, state_dir: Path) -> None:
        self._invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    async def list_tools(self) -> Sequence[SourceToolDefinition]:
        return [_ListedTool("report", "Write a report as a task")]

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition,
    ) -> DeferredToolCall:
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=turn.resolve_tool_policy(tool_name, ToolEffect.READ),
            tool_source_reference=json.dumps({"kind": "report"}),
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        if call.tool_call.tool_source_reference != json.dumps({"kind": "report"}):
            raise RuntimeError("report got another source's reference")
        if not call.supports_tool_tasks:
            raise RuntimeError("the runner does not poll tool tasks")
        task_id = f"task-{call.tool_call.tool_call_id}"
        self._invocations.record(f"start {task_id}")
        return ToolTaskStarted(task_id=task_id, poll_interval_seconds=1)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        record = poll.tool_call
        if record.tool_source_reference != json.dumps({"kind": "report"}):
            raise RuntimeError("report got another source's reference")
        task_id = record.task_id or ""
        earlier_polls = [
            name for name in self._invocations.names() if name.startswith(f"poll {task_id} ")
        ]
        poll_number = len(earlier_polls) + 1
        self._invocations.record(f"poll {task_id} {poll_number}")
        arguments = json.loads(record.arguments)
        if arguments.get("hold") or poll_number < int(arguments.get("polls", 1)):
            return ToolTaskPending(poll_interval_seconds=1)
        if arguments.get("fail"):
            raise ToolExecutionError(f"the report on {arguments['topic']} failed")
        return ToolExecutionResult(output=f"report on {arguments['topic']}")

    async def cancel_tool_task(self, cancellation: ToolTaskCancellation) -> None:
        record = cancellation.tool_call
        reason = record.cancellation_reason.value if record.cancellation_reason else "none"
        self._invocations.record(f"cancel-task {record.task_id} {record.status.value} {reason}")


def _run_model_limit_from_environment() -> RunModelLimit | None:
    limit = os.environ.get(RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE)
    if limit is None:
        return None
    method_timeout_seconds, maximum_attempts = limit.split(",")
    return RunModelLimit(
        method_timeout=timedelta(seconds=int(method_timeout_seconds)),
        maximum_attempts=int(maximum_attempts),
    )


class FileSpendLimit:
    """Logs every call, and refuses while ``SPEND_REFUSAL_FILE_NAME`` exists."""

    def __init__(self, state_dir: Path) -> None:
        self._state_dir = state_dir

    async def check_model_call(self, check: ModelCallCheck) -> ModelCallDecision:
        self._log(SPEND_CHECKS_LOG_NAME, check)
        refusal = self._state_dir / SPEND_REFUSAL_FILE_NAME
        return (
            ModelCallDecision.refuse(refusal.read_text())
            if refusal.exists()
            else ModelCallDecision.allow()
        )

    async def report_model_usage(self, report: ModelUsageReport) -> None:
        self._log(SPEND_REPORTS_LOG_NAME, report)

    def _log(self, file_name: str, entry: ModelCallCheck | ModelUsageReport) -> None:
        with (self._state_dir / file_name).open("a") as log:
            log.write(json.dumps(dataclasses.asdict(entry)) + "\n")


def _build_cli_environment(state_dir: Path, real_cli_environment: str | None) -> dict[str, str]:
    environment: dict[str, str] = {
        **json.loads(real_cli_environment or "{}"),
        "CLAUDE_CONFIG_DIR": str(state_dir / "claude-config"),
    }
    extra_environment = json.loads(os.environ.get(EXTRA_CLI_ENVIRONMENT_VARIABLE, "{}"))
    for name, value in extra_environment.items():
        if value is None:
            environment.pop(name, None)
        else:
            environment[name] = value
    return environment


def build_claude_test_agent_flow_from_environment() -> AgentSessionFlow:
    return build_claude_test_agent_flow(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


async def rewrite_publish_input(
    hook_input: HookInput, tool_use_id: str | None, context: HookContext
) -> HookJSONOutput:
    tool_input = cast(dict[str, Any], hook_input).get("tool_input") or {}
    if tool_input.get("message") != "rewrite me":
        return {}
    return cast(
        HookJSONOutput,
        {
            "hookSpecificOutput": {
                "hookEventName": "PreToolUse",
                "updatedInput": REWRITTEN_PUBLISH_INPUT,
            }
        },
    )


async def stamp_tool_input(
    hook_input: HookInput, tool_use_id: str | None, context: HookContext
) -> HookJSONOutput:
    tool_input = dict(cast(dict[str, Any], hook_input).get("tool_input") or {})
    field_name = "query" if "query" in tool_input else "message"
    if not str(tool_input.get(field_name, "")).startswith("stamp"):
        return {}
    tool_input[field_name] = f"{tool_input[field_name]}+"
    return cast(
        HookJSONOutput,
        {"hookSpecificOutput": {"hookEventName": "PreToolUse", "updatedInput": tool_input}},
    )


def _deny_while_flagged(state_dir: Path) -> HookCallback:
    async def deny_while_flagged(
        hook_input: HookInput, tool_use_id: str | None, context: HookContext
    ) -> HookJSONOutput:
        if not (state_dir / DENY_FLAG_FILE_NAME).exists():
            return {}
        return cast(
            HookJSONOutput,
            {
                "hookSpecificOutput": {
                    "hookEventName": "PreToolUse",
                    "permissionDecision": "deny",
                    "permissionDecisionReason": APPLICATION_HOOK_DENIAL,
                }
            },
        )

    return deny_while_flagged


async def raise_checked_payout(
    hook_input: HookInput, tool_use_id: str | None, context: HookContext
) -> HookJSONOutput:
    tool_input = dict(cast(dict[str, Any], hook_input).get("tool_input") or {})
    if tool_input.get("memo") != "raise":
        return {}
    tool_input["amount"] = RAISED_PAYOUT_AMOUNT
    return cast(
        HookJSONOutput,
        {"hookSpecificOutput": {"hookEventName": "PreToolUse", "updatedInput": tool_input}},
    )


async def rewrite_bash_command(
    hook_input: HookInput, tool_use_id: str | None, context: HookContext
) -> HookJSONOutput:
    tool_input = dict(cast(dict[str, Any], hook_input).get("tool_input") or {})
    if tool_input.get("command") != ORIGINAL_BASH_COMMAND:
        return {}
    tool_input["command"] = REWRITTEN_BASH_COMMAND
    return cast(
        HookJSONOutput,
        {"hookSpecificOutput": {"hookEventName": "PreToolUse", "updatedInput": tool_input}},
    )


def build_custom_tools(state_dir: Path) -> list[SdkMcpTool[Any]]:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    @tool(
        "lookup",
        "Look up a record",
        {"query": str},
        annotations=ToolAnnotations(readOnlyHint=True),
    )
    async def lookup(arguments: dict[str, Any]) -> dict[str, Any]:
        invocations.record("lookup")
        return _text_result(f"found {arguments['query']}")

    @tool("publish", "Publish a message", {"message": str})
    async def publish(arguments: dict[str, Any]) -> dict[str, Any]:
        invocations.record("publish")
        return _text_result(f"published {arguments['message']}")

    @tool(
        "charge",
        "Charge an amount",
        {
            "type": "object",
            "properties": {"amount": {"type": "integer"}, "fail_attempts": {"type": "integer"}},
            "required": ["amount"],
        },
        annotations=ToolAnnotations(idempotentHint=True),
    )
    async def charge(arguments: dict[str, Any]) -> dict[str, Any]:
        invocations.record("charge")
        execution = current_deferred_tool_execution()
        if execution is None:
            raise RuntimeError("charge must run in a deferred tool Step")
        if execution.attempt <= int(arguments.get("fail_attempts", 0)):
            raise RuntimeError(f"charge attempt {execution.attempt} lost its connection")
        if int(arguments["amount"]) <= 0:
            return _text_result("amount must be positive", is_error=True)
        return _text_result(f"charged {arguments['amount']} with key {execution.idempotency_key}")

    @tool("slow_write", "Write slowly", {"message": str})
    async def slow_write(arguments: dict[str, Any]) -> dict[str, Any]:
        invocations.record("slow_write")
        gate = FileGate(state_dir, SLOW_WRITE_GATE)
        gate.mark_reached()
        await gate.wait_for_release()
        return _text_result(f"wrote {arguments['message']}")

    return [lookup, publish, charge, slow_write]


def _text_result(text: str, *, is_error: bool = False) -> dict[str, Any]:
    return {"content": [{"type": "text", "text": text}], "is_error": is_error}
