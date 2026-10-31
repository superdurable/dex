# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The permission decisions of one turn segment: run inline, defer, or replay a record."""

from __future__ import annotations

import json
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass, replace
from typing import Any, cast

import jsonschema
from claude_agent_sdk import (
    DeferredToolUse,
    HookContext,
    HookInput,
    HookJSONOutput,
    PermissionResult,
    PermissionResultAllow,
    PermissionResultDeny,
    SdkMcpTool,
    ToolPermissionContext,
    _build_input_schema,
)

from dex_agents import (
    AgentToolCall,
    AgentTurnContext,
    DeferredToolCall,
    ToolCallStatus,
    ToolEffect,
    ToolPolicy,
    describe_tool_call_outcome,
)

#: Names of tools that run inside the Claude Code 2.1 CLI. A custom tool may not
#: use one: records and policies name a custom tool by its own name, so a shared
#: name would confuse the two. ``ClaudeAgentRunner`` rejects such names, and a
#: segment that meets a newer built-in with a custom tool's name fails.
CLAUDE_CODE_BUILT_IN_TOOL_NAMES = frozenset(
    {
        "Agent",
        "AskUserQuestion",
        "Bash",
        "BashOutput",
        "Config",
        "CronCreate",
        "CronDelete",
        "CronList",
        "Edit",
        "EnterPlanMode",
        "EnterWorktree",
        "ExitPlanMode",
        "ExitWorktree",
        "Glob",
        "Grep",
        "KillShell",
        "LS",
        "ListAgents",
        "ListMcpResources",
        "LSP",
        "Monitor",
        "MultiEdit",
        "NotebookEdit",
        "NotebookRead",
        "Read",
        "ReadMcpResource",
        "ReportFindings",
        "ScheduleWakeup",
        "SendMessage",
        "Skill",
        "SlashCommand",
        "Task",
        "TaskOutput",
        "TaskStop",
        "TodoWrite",
        "ToolSearch",
        "WebFetch",
        "WebSearch",
        "Workflow",
        "Write",
    }
)
CLAUDE_CODE_TOOL_USE_ID_META_KEY = "claudecode/toolUseId"
ONE_DEFERRAL_PER_REPLY_MESSAGE = (
    "{tool_name} did not run. Only one tool call per reply can run outside this turn, "
    "and another call already does. Call {tool_name} again by itself."
)
SUBAGENT_DEFERRAL_REFUSED_MESSAGE = (
    "{tool_name} did not run. It must run outside this turn, which a subagent cannot do. "
    "Call it from the main conversation instead."
)
UNPERMITTED_TOOL_CALL_MESSAGE = (
    "{tool_name} did not run: the session's tool policies do not permit this call."
)
BUILT_IN_TOOL_NAME_COLLISION_MESSAGE = (
    "{tool_name} did not run: a custom tool and a Claude Code tool share this name."
)


class ToolCatalog:
    def __init__(
        self, custom_tools: Sequence[SdkMcpTool[Any]], custom_tool_server_name: str
    ) -> None:
        self.custom_tool_server_name = custom_tool_server_name
        self._custom_tool_prefix = f"mcp__{custom_tool_server_name}__"
        self._custom_tools = {custom_tool.name: custom_tool for custom_tool in custom_tools}
        if len(self._custom_tools) != len(custom_tools):
            raise ValueError("custom tool names must be unique")
        colliding_names = sorted(set(self._custom_tools) & CLAUDE_CODE_BUILT_IN_TOOL_NAMES)
        if colliding_names:
            raise ValueError(
                f"custom tool names {colliding_names} are Claude Code tool names; rename them"
            )
        self._input_schemas = {
            name: _build_input_schema(custom_tool)
            for name, custom_tool in self._custom_tools.items()
        }

    @property
    def custom_tools(self) -> list[SdkMcpTool[Any]]:
        return list(self._custom_tools.values())

    def custom_tool(self, custom_tool_name: str) -> SdkMcpTool[Any] | None:
        return self._custom_tools.get(custom_tool_name)

    def input_schema(self, custom_tool_name: str) -> dict[str, Any]:
        return self._input_schemas[custom_tool_name]

    def custom_tool_name_for(self, cli_tool_name: str) -> str | None:
        # Classify by the CLI name: only mcp__<server>__<name> is a custom tool.
        if not cli_tool_name.startswith(self._custom_tool_prefix):
            return None
        custom_tool_name = cli_tool_name.removeprefix(self._custom_tool_prefix)
        return custom_tool_name if custom_tool_name in self._custom_tools else None

    def policy_tool_name(self, cli_tool_name: str) -> str:
        return self.custom_tool_name_for(cli_tool_name) or cli_tool_name

    def framework_effect(self, custom_tool_name: str | None) -> ToolEffect | None:
        custom_tool = None if custom_tool_name is None else self._custom_tools[custom_tool_name]
        if custom_tool is None or custom_tool.annotations is None:
            return None
        hints = custom_tool.annotations.model_dump(by_alias=True, exclude_none=True)
        if hints.get("readOnlyHint") is True:
            return ToolEffect.READ
        if hints.get("idempotentHint") is True:
            return ToolEffect.IDEMPOTENT_WRITE
        return None

    def requires_deferral(self, is_custom_tool: bool, policy: ToolPolicy) -> bool:
        # Custom reads run inline: each deferral restarts the CLI for one more model call.
        if is_custom_tool:
            return policy.effect is not ToolEffect.READ or policy.is_approval_required
        # A CLI tool defers only for approval: the adapter cannot record its run.
        return policy.is_approval_required

    def validate_custom_tool_input(self, custom_tool_name: str, arguments: object) -> str | None:
        try:
            jsonschema.validate(instance=arguments, schema=self._input_schemas[custom_tool_name])
        except jsonschema.ValidationError as error:
            return f"Input validation error: {error.message}"
        return None


@dataclass(frozen=True)
class _CustomToolCallPlan:
    custom_tool_name: str
    recorded_tool_call: AgentToolCall | None


class ToolCallGate:
    def __init__(self, turn: AgentTurnContext, tool_catalog: ToolCatalog) -> None:
        self._turn = turn
        self._tool_catalog = tool_catalog
        self._segment_deferral: DeferredToolCall | None = None
        self._permitted_tool_use_ids: set[str] = set()
        self._running_tool_names: dict[str, str] = {}
        self._custom_tool_call_plans: dict[str, _CustomToolCallPlan] = {}
        self.built_in_tool_name_collisions: set[str] = set()

    @property
    def segment_deferral(self) -> DeferredToolCall | None:
        return self._segment_deferral

    def resolve_policy(self, cli_tool_name: str) -> ToolPolicy:
        custom_tool_name = self._tool_catalog.custom_tool_name_for(cli_tool_name)
        return self._turn.resolve_tool_policy(
            custom_tool_name or cli_tool_name, self._tool_catalog.framework_effect(custom_tool_name)
        )

    async def decide_pre_tool_use(
        self,
        hook_input: HookInput,
        tool_use_id: str | None,
        context: HookContext,
    ) -> HookJSONOutput:
        fields = cast(dict[str, Any], hook_input)
        if fields.get("hook_event_name") != "PreToolUse":
            return {}
        cli_tool_name = str(fields["tool_name"])
        tool_input: dict[str, Any] = dict(fields.get("tool_input") or {})
        call_id = str(fields.get("tool_use_id") or tool_use_id or "")
        if not call_id:
            raise RuntimeError(f"the CLI sent PreToolUse for {cli_tool_name} without a tool_use_id")
        custom_tool_name = self._tool_catalog.custom_tool_name_for(cli_tool_name)
        if custom_tool_name is None and self._tool_catalog.custom_tool(cli_tool_name) is not None:
            self.built_in_tool_name_collisions.add(cli_tool_name)
            return _pre_tool_use_decision(
                "deny", BUILT_IN_TOOL_NAME_COLLISION_MESSAGE.format(tool_name=cli_tool_name)
            )
        policy_tool_name = custom_tool_name or cli_tool_name
        record = self._turn.recorded_tool_call(call_id)
        if record is not None:
            return self._replay_recorded_tool_call(record, custom_tool_name)
        policy = self.resolve_policy(cli_tool_name)
        if not self._tool_catalog.requires_deferral(custom_tool_name is not None, policy):
            self._permit_inline_tool_call(call_id, policy_tool_name, custom_tool_name)
            return _pre_tool_use_decision("allow")
        if fields.get("agent_id"):
            return _pre_tool_use_decision(
                "deny", SUBAGENT_DEFERRAL_REFUSED_MESSAGE.format(tool_name=policy_tool_name)
            )
        # The CLI resumes only one deferred call, so later ones in the segment must wait.
        if self._segment_deferral is not None:
            return _pre_tool_use_decision(
                "deny", ONE_DEFERRAL_PER_REPLY_MESSAGE.format(tool_name=policy_tool_name)
            )
        self._segment_deferral = DeferredToolCall(
            tool_call_id=call_id,
            tool_name=policy_tool_name,
            arguments=json.dumps(tool_input, ensure_ascii=False),
            policy=policy,
        )
        return _pre_tool_use_decision("defer", "The Dex agent session settles this call.")

    async def decide_tool_permission(
        self,
        cli_tool_name: str,
        tool_input: dict[str, Any],
        context: ToolPermissionContext,
    ) -> PermissionResult:
        policy_tool_name = self._tool_catalog.policy_tool_name(cli_tool_name)
        call_id = context.tool_use_id or ""
        record = self._turn.recorded_tool_call(call_id)
        # A resumed call can skip the hook if the CLI starts it before the hooks exist.
        if record is not None and call_id not in self._permitted_tool_use_ids:
            custom_tool_name = self._tool_catalog.custom_tool_name_for(cli_tool_name)
            return _permission_result(self._replay_recorded_tool_call(record, custom_tool_name))
        # The CLI asks for permission when it ignores a defer, so that call never left the turn.
        if self._segment_deferral is not None and self._segment_deferral.tool_call_id == call_id:
            self._segment_deferral = None
            return PermissionResultDeny(
                message=ONE_DEFERRAL_PER_REPLY_MESSAGE.format(tool_name=policy_tool_name)
            )
        if call_id in self._permitted_tool_use_ids:
            return PermissionResultAllow()
        return PermissionResultDeny(
            message=UNPERMITTED_TOOL_CALL_MESSAGE.format(tool_name=policy_tool_name)
        )

    async def call_custom_tool(
        self,
        custom_tool_name: str,
        tool_use_id: str | None,
        arguments: dict[str, Any],
        run_handler: Callable[[dict[str, Any]], Awaitable[dict[str, Any]]],
    ) -> dict[str, Any]:
        plan = None if tool_use_id is None else self._custom_tool_call_plans.pop(tool_use_id, None)
        if plan is None or plan.custom_tool_name != custom_tool_name:
            return _text_tool_result(
                UNPERMITTED_TOOL_CALL_MESSAGE.format(tool_name=custom_tool_name), is_error=True
            )
        if plan.recorded_tool_call is not None:
            return recorded_tool_call_result(plan.recorded_tool_call)
        validation_error = self._tool_catalog.validate_custom_tool_input(
            custom_tool_name, arguments
        )
        if validation_error is not None:
            return _text_tool_result(validation_error, is_error=True)
        return await run_handler(arguments)

    def record_tool_result(self, tool_use_id: str, is_error: bool) -> None:
        tool_name = self._running_tool_names.pop(tool_use_id, None)
        if tool_name is not None:
            status = ToolCallStatus.FAILED if is_error else ToolCallStatus.SUCCEEDED
            self._turn.events.write_tool_call_finished(tool_use_id, tool_name, status)

    def deferred_tool_call(self, deferred_tool_use: DeferredToolUse) -> DeferredToolCall:
        if (
            self._segment_deferral is None
            or self._segment_deferral.tool_call_id != deferred_tool_use.id
        ):
            requested = (
                None if self._segment_deferral is None else self._segment_deferral.tool_call_id
            )
            raise RuntimeError(
                f"the CLI deferred tool call {deferred_tool_use.id}, but the adapter deferred "
                f"{requested}"
            )
        # The CLI records the input after every hook's updatedInput; the Step must use it.
        return replace(
            self._segment_deferral,
            arguments=json.dumps(deferred_tool_use.input, ensure_ascii=False),
        )

    def _replay_recorded_tool_call(
        self, record: AgentToolCall, custom_tool_name: str | None
    ) -> HookJSONOutput:
        if not record.status.is_final:
            raise RuntimeError(f"tool call {record.tool_call_id} resumed before it was final")
        # The CLI prefixes a hook denial, so a custom tool returns even its rejection itself.
        if custom_tool_name is not None:
            self._custom_tool_call_plans[record.tool_call_id] = _CustomToolCallPlan(
                custom_tool_name, record
            )
            self._permitted_tool_use_ids.add(record.tool_call_id)
            return _pre_tool_use_decision("allow")
        if record.status is ToolCallStatus.SUCCEEDED:
            self._permit_inline_tool_call(record.tool_call_id, record.tool_name, None)
            return _pre_tool_use_decision("allow")
        return _pre_tool_use_decision("deny", describe_tool_call_outcome(record))

    def _permit_inline_tool_call(
        self, tool_use_id: str, policy_tool_name: str, custom_tool_name: str | None
    ) -> None:
        self._permitted_tool_use_ids.add(tool_use_id)
        if custom_tool_name is not None:
            self._custom_tool_call_plans[tool_use_id] = _CustomToolCallPlan(custom_tool_name, None)
        self._running_tool_names[tool_use_id] = policy_tool_name
        self._turn.events.write_tool_call_started(tool_use_id, policy_tool_name)


def recorded_tool_call_result(record: AgentToolCall) -> dict[str, Any]:
    return _text_tool_result(
        describe_tool_call_outcome(record),
        is_error=record.status is not ToolCallStatus.SUCCEEDED,
    )


def _text_tool_result(text: str, *, is_error: bool) -> dict[str, Any]:
    return {"content": [{"type": "text", "text": text}], "is_error": is_error}


def _permission_result(hook_output: HookJSONOutput) -> PermissionResult:
    hook_specific_output = cast(dict[str, Any], hook_output).get("hookSpecificOutput") or {}
    if hook_specific_output.get("permissionDecision") == "allow":
        return PermissionResultAllow()
    return PermissionResultDeny(
        message=str(hook_specific_output.get("permissionDecisionReason", ""))
    )


def _pre_tool_use_decision(decision: str, reason: str | None = None) -> HookJSONOutput:
    hook_specific_output: dict[str, Any] = {
        "hookEventName": "PreToolUse",
        "permissionDecision": decision,
    }
    if reason is not None:
        hook_specific_output["permissionDecisionReason"] = reason
    return cast(HookJSONOutput, {"hookSpecificOutput": hook_specific_output})
