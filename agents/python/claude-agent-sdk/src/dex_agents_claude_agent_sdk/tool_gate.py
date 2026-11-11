# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The permission decisions of one turn segment: run inline, defer, or replay a record."""

from __future__ import annotations

import base64
import json
from collections.abc import Awaitable, Callable, Collection, Mapping, Sequence
from dataclasses import dataclass, replace
from typing import Any, cast

import jsonschema
from claude_agent_sdk import (
    DeferredToolUse,
    HookCallback,
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
    MediaStore,
    ToolCallStatus,
    ToolEffect,
    ToolPolicy,
    describe_tool_call_outcome,
    load_images,
)
from dex_agents_claude_agent_sdk.tool_sources import ListedSourceTool

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
ORDERED_WRITE_DEFERRAL_MESSAGE = (
    "{tool_name} did not run. Writes run one at a time in your order, and an earlier call "
    "of this reply runs first. Read its result, then call {tool_name} again by itself "
    "if it should still run."
)
UNLISTED_SOURCE_TOOL_MESSAGE = (
    "{tool_name} did not run: its tool source does not offer this tool now."
)
BUILT_IN_TOOL_NAME_COLLISION_MESSAGE = (
    "{tool_name} did not run: a custom tool and a Claude Code tool share this name."
)
MISMATCHED_REPLAY_MESSAGE = (
    "{tool_name} did not run: tool use ID {tool_use_id} belongs to a recorded call of "
    "{recorded_tool_name}."
)
REPEATED_REPLAY_MESSAGE = (
    "{tool_name} did not run: the recorded call {tool_use_id} was already resumed in this "
    "turn segment."
)
DECIDED_TOOL_USE_ID_MESSAGE = (
    "{tool_name} did not run: tool use ID {tool_use_id} was already decided in this turn "
    "segment."
)
SETTLED_REPLAY_MESSAGE = (
    "{tool_name} did not run: the recorded call {tool_use_id} was already resumed in an "
    "earlier turn segment."
)


class ToolCatalog:
    def __init__(
        self,
        custom_tools: Sequence[SdkMcpTool[Any]],
        custom_tool_server_name: str,
        source_tools: Mapping[str, ListedSourceTool] | None = None,
        replay_only_tool_names: Collection[str] = (),
    ) -> None:
        self.custom_tool_server_name = custom_tool_server_name
        self._custom_tool_prefix = f"mcp__{custom_tool_server_name}__"
        self._custom_tools = {custom_tool.name: custom_tool for custom_tool in custom_tools}
        # Listed per segment; ToolSourceRouter refuses their name collisions.
        self._source_tools = dict(source_tools or {})
        # Recorded source tools this listing lacks: served only to replay their records.
        self._replay_only_tool_names = frozenset(
            tool_name
            for tool_name in replay_only_tool_names
            if tool_name not in self._custom_tools and tool_name not in self._source_tools
        )
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

    def with_source_tools(
        self,
        source_tools: Mapping[str, ListedSourceTool],
        replay_only_tool_names: Collection[str] = (),
    ) -> ToolCatalog:
        return ToolCatalog(
            self.custom_tools, self.custom_tool_server_name, source_tools, replay_only_tool_names
        )

    @property
    def custom_tools(self) -> list[SdkMcpTool[Any]]:
        return list(self._custom_tools.values())

    @property
    def source_tools(self) -> list[ListedSourceTool]:
        return list(self._source_tools.values())

    @property
    def replay_only_tool_names(self) -> list[str]:
        return sorted(self._replay_only_tool_names)

    @property
    def has_served_tools(self) -> bool:
        return bool(self._custom_tools or self._source_tools or self._replay_only_tool_names)

    def custom_tool(self, custom_tool_name: str) -> SdkMcpTool[Any] | None:
        return self._custom_tools.get(custom_tool_name)

    def source_tool(self, tool_name: str) -> ListedSourceTool | None:
        return self._source_tools.get(tool_name)

    def is_replay_only_tool(self, tool_name: str) -> bool:
        return tool_name in self._replay_only_tool_names

    def is_served_tool_name(self, tool_name: str) -> bool:
        return (
            tool_name in self._custom_tools
            or tool_name in self._source_tools
            or tool_name in self._replay_only_tool_names
        )

    def input_schema(self, custom_tool_name: str) -> dict[str, Any]:
        return self._input_schemas[custom_tool_name]

    def custom_tool_name_for(self, cli_tool_name: str) -> str | None:
        served_tool_name = self.served_tool_name_for(cli_tool_name)
        return served_tool_name if served_tool_name in self._custom_tools else None

    def served_tool_name_for(self, cli_tool_name: str) -> str | None:
        # Classify by the CLI name: only mcp__<server>__<name> is a custom or source tool.
        if not cli_tool_name.startswith(self._custom_tool_prefix):
            return None
        tool_name = cli_tool_name.removeprefix(self._custom_tool_prefix)
        return tool_name if self.is_served_tool_name(tool_name) else None

    def policy_tool_name(self, cli_tool_name: str) -> str:
        return self.served_tool_name_for(cli_tool_name) or cli_tool_name

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

    def requires_deferral(self, is_custom_tool: bool, policy: ToolPolicy | None) -> bool:
        # A source tool has no policy here and always runs in the tool Step its source needs.
        if policy is None:
            return True
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
    def __init__(
        self,
        turn: AgentTurnContext,
        tool_catalog: ToolCatalog,
        settled_tool_use_ids: Collection[str],
        media_store: MediaStore | None = None,
    ) -> None:
        self._turn = turn
        self._tool_catalog = tool_catalog
        self._media_store = media_store
        # Calls the stored transcript already answers: only a pending deferral may replay.
        self._settled_tool_use_ids = frozenset(settled_tool_use_ids)
        self._replayed_tool_use_ids: set[str] = set()
        self._decided_tool_use_ids: set[str] = set()
        self._segment_deferral: DeferredToolCall | None = None
        self._segment_deferral_source_tool: ListedSourceTool | None = None
        self._running_tool_names: dict[str, str] = {}
        self._custom_tool_call_plans: dict[str, _CustomToolCallPlan] = {}
        self.built_in_tool_name_collisions: set[str] = set()

    @property
    def segment_deferral(self) -> DeferredToolCall | None:
        return self._segment_deferral

    def resolve_policy(self, cli_tool_name: str) -> ToolPolicy | None:
        """Return the policy of a custom or CLI tool, or ``None`` for a source tool."""
        served_tool_name = self._tool_catalog.served_tool_name_for(cli_tool_name)
        if served_tool_name is not None and (
            self._tool_catalog.source_tool(served_tool_name)
            or self._tool_catalog.is_replay_only_tool(served_tool_name)
        ):
            return None
        custom_tool_name = self._tool_catalog.custom_tool_name_for(cli_tool_name)
        return self._turn.resolve_tool_policy(
            custom_tool_name or cli_tool_name, self._tool_catalog.framework_effect(custom_tool_name)
        )

    def skip_hook_on_replay(self, callback: HookCallback) -> HookCallback:
        """Wrap an application PreToolUse callback so it never sees a call with a final record."""

        async def call_unless_replayed(
            hook_input: HookInput, tool_use_id: str | None, context: HookContext
        ) -> HookJSONOutput:
            fields = cast(dict[str, Any], hook_input)
            call_id = str(fields.get("tool_use_id") or tool_use_id or "")
            record = self._turn.recorded_tool_call(call_id) if call_id else None
            if record is not None and record.status.is_final:
                return {}
            return await callback(hook_input, tool_use_id, context)

        return call_unless_replayed

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
        # One decision per tool use ID and segment: a later one never replaces it.
        is_decided = call_id in self._decided_tool_use_ids
        self._decided_tool_use_ids.add(call_id)
        served_tool_name = self._tool_catalog.served_tool_name_for(cli_tool_name)
        if served_tool_name is None and self._tool_catalog.is_served_tool_name(cli_tool_name):
            self.built_in_tool_name_collisions.add(cli_tool_name)
            return _pre_tool_use_decision(
                "deny", BUILT_IN_TOOL_NAME_COLLISION_MESSAGE.format(tool_name=cli_tool_name)
            )
        policy_tool_name = served_tool_name or cli_tool_name
        record = self._turn.recorded_tool_call(call_id)
        if record is not None:
            return self._replay_recorded_tool_call(record, policy_tool_name, served_tool_name)
        if is_decided:
            return _pre_tool_use_decision(
                "deny",
                DECIDED_TOOL_USE_ID_MESSAGE.format(tool_name=policy_tool_name, tool_use_id=call_id),
            )
        if served_tool_name is not None and self._tool_catalog.is_replay_only_tool(
            served_tool_name
        ):
            # A new call would run under a new Call ID; only the source's listing permits that.
            return _pre_tool_use_decision(
                "deny", UNLISTED_SOURCE_TOOL_MESSAGE.format(tool_name=policy_tool_name)
            )
        source_tool = (
            None if served_tool_name is None else self._tool_catalog.source_tool(served_tool_name)
        )
        custom_tool_name = None if source_tool is not None else served_tool_name
        policy = self.resolve_policy(cli_tool_name)
        if not self._tool_catalog.requires_deferral(custom_tool_name is not None, policy):
            self._permit_inline_tool_call(call_id, policy_tool_name, custom_tool_name)
            return _pre_tool_use_decision("allow")
        if fields.get("agent_id"):
            return _pre_tool_use_decision(
                "deny", SUBAGENT_DEFERRAL_REFUSED_MESSAGE.format(tool_name=policy_tool_name)
            )
        arguments = json.dumps(tool_input, ensure_ascii=False)
        deferral = (
            source_tool.build_deferred_tool_call(self._turn, call_id, arguments)
            if source_tool is not None
            else DeferredToolCall(
                tool_call_id=call_id,
                tool_name=policy_tool_name,
                arguments=arguments,
                policy=_require_policy(policy),
            )
        )
        # The CLI resumes only one deferred call, so later ones in the segment must wait.
        if self._segment_deferral is not None:
            message = (
                ORDERED_WRITE_DEFERRAL_MESSAGE
                if deferral.ordered
                else ONE_DEFERRAL_PER_REPLY_MESSAGE
            )
            return _pre_tool_use_decision("deny", message.format(tool_name=policy_tool_name))
        self._segment_deferral = deferral
        self._segment_deferral_source_tool = source_tool
        return _pre_tool_use_decision("defer", "The Dex agent session settles this call.")

    async def decide_tool_permission(
        self,
        cli_tool_name: str,
        tool_input: dict[str, Any],
        context: ToolPermissionContext,
    ) -> PermissionResult:
        """Answer a permission ask; only a replay the hook did not decide may be allowed."""
        policy_tool_name = self._tool_catalog.policy_tool_name(cli_tool_name)
        call_id = context.tool_use_id or ""
        record = self._turn.recorded_tool_call(call_id)
        # A resumed call can skip the hook if the CLI starts it before the hooks exist.
        if record is not None:
            served_tool_name = self._tool_catalog.served_tool_name_for(cli_tool_name)
            return _permission_result(
                self._replay_recorded_tool_call(record, policy_tool_name, served_tool_name)
            )
        # The CLI asks for permission when it ignores a defer, so that call never left the turn.
        if self._segment_deferral is not None and self._segment_deferral.tool_call_id == call_id:
            self._segment_deferral = None
            self._segment_deferral_source_tool = None
            return PermissionResultDeny(
                message=ONE_DEFERRAL_PER_REPLY_MESSAGE.format(tool_name=policy_tool_name)
            )
        # A hook allow is the call's one decision, so no later ask confirms it.
        return PermissionResultDeny(
            message=UNPERMITTED_TOOL_CALL_MESSAGE.format(tool_name=policy_tool_name)
        )

    async def call_custom_tool(
        self,
        custom_tool_name: str,
        tool_use_id: str | None,
        arguments: dict[str, Any],
        run_handler: Callable[[dict[str, Any]], Awaitable[dict[str, Any]]] | None,
    ) -> dict[str, Any]:
        # A source tool has no run_handler: the server only replays its record.
        plan = None if tool_use_id is None else self._custom_tool_call_plans.pop(tool_use_id, None)
        if plan is None or plan.custom_tool_name != custom_tool_name:
            return _text_tool_result(
                UNPERMITTED_TOOL_CALL_MESSAGE.format(tool_name=custom_tool_name), is_error=True
            )
        if plan.recorded_tool_call is not None:
            return await self._recorded_tool_call_result_with_images(plan.recorded_tool_call)
        if run_handler is None:
            return _text_tool_result(
                UNPERMITTED_TOOL_CALL_MESSAGE.format(tool_name=custom_tool_name), is_error=True
            )
        validation_error = self._tool_catalog.validate_custom_tool_input(
            custom_tool_name, arguments
        )
        if validation_error is not None:
            return _text_tool_result(validation_error, is_error=True)
        return await run_handler(arguments)

    async def _recorded_tool_call_result_with_images(self, record: AgentToolCall) -> dict[str, Any]:
        result = recorded_tool_call_result(record)
        if self._media_store is None or not record.images:
            return result
        # MCP image content, which the CLI passes to the model inside the tool_result.
        images = await load_images(self._media_store, record.images)
        result["content"] += [
            {
                "type": "image",
                "data": base64.b64encode(images[image.sha256]).decode("ascii"),
                "mimeType": image.mime_type,
            }
            for image in record.images
        ]
        return result

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
        arguments = json.dumps(deferred_tool_use.input, ensure_ascii=False)
        if self._segment_deferral_source_tool is not None:
            # Rebuilt, so the source decides approval from the arguments its Step runs.
            return self._segment_deferral_source_tool.build_deferred_tool_call(
                self._turn, deferred_tool_use.id, arguments
            )
        return replace(self._segment_deferral, arguments=arguments)

    def _replay_recorded_tool_call(
        self, record: AgentToolCall, policy_tool_name: str, served_tool_name: str | None
    ) -> HookJSONOutput:
        """Replay the pending deferral once, as its own tool; deny any other use of its ID."""
        call_id = record.tool_call_id
        if call_id in self._replayed_tool_use_ids:
            return _pre_tool_use_decision(
                "deny",
                REPEATED_REPLAY_MESSAGE.format(tool_name=policy_tool_name, tool_use_id=call_id),
            )
        if not record.status.is_final:
            raise RuntimeError(f"tool call {call_id} resumed before it was final")
        self._replayed_tool_use_ids.add(call_id)
        if call_id in self._settled_tool_use_ids:
            return _pre_tool_use_decision(
                "deny",
                SETTLED_REPLAY_MESSAGE.format(tool_name=policy_tool_name, tool_use_id=call_id),
            )
        # A served record never replays as a CLI tool, nor the reverse, so its input stays put.
        is_served_record = self._tool_catalog.is_served_tool_name(record.tool_name)
        if record.tool_name != policy_tool_name or is_served_record != (
            served_tool_name is not None
        ):
            return _pre_tool_use_decision(
                "deny",
                MISMATCHED_REPLAY_MESSAGE.format(
                    tool_name=policy_tool_name,
                    tool_use_id=call_id,
                    recorded_tool_name=record.tool_name,
                ),
            )
        # The CLI prefixes a hook denial, so a custom tool returns even its rejection itself.
        # Application hooks are skipped on replay, so every allow carries the approved input.
        if served_tool_name is not None:
            recorded_input = _recorded_tool_input(record)
            self._custom_tool_call_plans[call_id] = _CustomToolCallPlan(served_tool_name, record)
            return _pre_tool_use_decision("allow", updated_input=recorded_input)
        if record.status is ToolCallStatus.SUCCEEDED:
            recorded_input = _recorded_tool_input(record)
            self._permit_inline_tool_call(call_id, record.tool_name, None)
            return _pre_tool_use_decision("allow", updated_input=recorded_input)
        return _pre_tool_use_decision("deny", describe_tool_call_outcome(record))

    def _permit_inline_tool_call(
        self, tool_use_id: str, policy_tool_name: str, custom_tool_name: str | None
    ) -> None:
        if custom_tool_name is not None:
            self._custom_tool_call_plans[tool_use_id] = _CustomToolCallPlan(custom_tool_name, None)
        self._running_tool_names[tool_use_id] = policy_tool_name
        self._turn.events.write_tool_call_started(tool_use_id, policy_tool_name)


def _require_policy(policy: ToolPolicy | None) -> ToolPolicy:
    if policy is None:
        raise RuntimeError("a custom or Claude Code tool call has no policy")
    return policy


def recorded_tool_call_result(record: AgentToolCall) -> dict[str, Any]:
    return _text_tool_result(
        describe_tool_call_outcome(record),
        is_error=record.status is not ToolCallStatus.SUCCEEDED,
    )


def _text_tool_result(text: str, *, is_error: bool) -> dict[str, Any]:
    return {"content": [{"type": "text", "text": text}], "is_error": is_error}


def _recorded_tool_input(record: AgentToolCall) -> dict[str, Any]:
    recorded_input = json.loads(record.arguments)
    if not isinstance(recorded_input, dict):
        raise RuntimeError(f"tool call {record.tool_call_id} recorded non-object arguments")
    return recorded_input


def _permission_result(hook_output: HookJSONOutput) -> PermissionResult:
    """Convert a replay decision; an allow without the recorded input is denied."""
    hook_specific_output = cast(dict[str, Any], hook_output).get("hookSpecificOutput") or {}
    updated_input = hook_specific_output.get("updatedInput")
    if hook_specific_output.get("permissionDecision") == "allow" and isinstance(
        updated_input, dict
    ):
        return PermissionResultAllow(updated_input=updated_input)
    return PermissionResultDeny(
        message=str(hook_specific_output.get("permissionDecisionReason", ""))
    )


def _pre_tool_use_decision(
    decision: str, reason: str | None = None, updated_input: dict[str, Any] | None = None
) -> HookJSONOutput:
    hook_specific_output: dict[str, Any] = {
        "hookEventName": "PreToolUse",
        "permissionDecision": decision,
    }
    if reason is not None:
        hook_specific_output["permissionDecisionReason"] = reason
    if updated_input is not None:
        hook_specific_output["updatedInput"] = updated_input
    return cast(HookJSONOutput, {"hookSpecificOutput": hook_specific_output})
