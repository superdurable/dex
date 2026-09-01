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

"""A durable general-purpose AI Agent built with Dex primitives."""

from __future__ import annotations

import json
from dataclasses import replace
from datetime import timedelta
from typing import Any

from dex import (
    AsyncContext,
    Attribute,
    AttributeMap,
    Channel,
    ChannelMap,
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Stream,
    Timer,
    Wait,
    go_to,
    rpc,
)

from dex_examples.products.ai_agent.mcp_registry import MCPRegistry
from dex_examples.products.ai_agent.model_client import ModelClient
from dex_examples.products.ai_agent.models import (
    AgentConfig,
    AgentDescription,
    AgentEvent,
    AgentMessage,
    AgentState,
    ContextSummary,
    HistoryPage,
    HistoryRequest,
    PendingApproval,
    PendingTimer,
    SequencedMessage,
    ToolApproval,
    ToolApprovalRequest,
    ToolCall,
    ToolDefinition,
    ToolExecutionResult,
    UserMessage,
)

STATUS_WAITING = "waiting_for_message"
STATUS_COMPACTING = "compacting_context"
STATUS_CALLING_MODEL = "calling_model"
STATUS_ROUTING_TOOL = "routing_tool"
STATUS_WAITING_APPROVAL = "waiting_for_tool_approval"
STATUS_EXECUTING_TOOL = "executing_tool"
STATUS_WAITING_TIMER = "waiting_for_timer"

MODEL_OPTIONS = StepOptions(
    execute_method_timeout=timedelta(minutes=10),
    heartbeat_timeout=timedelta(minutes=5),
    execute_retry=RetryPolicy(
        maximum_attempts=3,
        total_duration=timedelta(minutes=30),
    ),
)
TOOL_OPTIONS = StepOptions(
    execute_method_timeout=timedelta(hours=2),
    heartbeat_timeout=timedelta(minutes=5),
    execute_retry=RetryPolicy(maximum_attempts=1),
)


class Init(Step[AgentConfig]):
    def __init__(self, flow: AIAgentFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, input: AgentConfig) -> StepDecision:
        input.validate()
        self.flow.validate_config(input)
        self.flow.config.set(context, input)
        self.flow.state.set(context, AgentState(status=STATUS_WAITING))
        return go_to(AwaitUser, None)


class AwaitUser(Step[None]):
    def __init__(self, flow: AIAgentFlow) -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: None) -> Wait:
        self.flow.update_status(context, STATUS_WAITING)
        return Wait.until(self.flow.user_messages.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        messages = self.flow.user_messages.results(context)
        if not messages:
            raise RuntimeError("the user-message wait completed without a message")
        self.flow.append_message(context, AgentMessage("user", messages[0].content))
        return go_to(CompactContext, None)


class CompactContext(Step[None]):
    def __init__(self, flow: AIAgentFlow) -> None:
        self.flow = flow

    def get_step_options(self) -> StepOptions:
        return MODEL_OPTIONS

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> StepDecision:
        self.flow.update_status(context, STATUS_COMPACTING)
        config = self.flow.config.get(context)
        state = self.flow.state.get(context)
        state = self.flow.trim_summarized_messages(context, config, state)
        context_messages = self.flow.context_messages(context, config, state)
        count_messages = [AgentMessage("system", config.system_prompt), *context_messages]
        token_count = self.flow.model_client.count_tokens(config.model, count_messages)
        has_retention_pressure = (
            state.last_sequence - state.first_retained_sequence + 1
            > config.message_retention_limit
        )
        if (
            token_count
            < int(config.max_context_tokens * config.compaction_trigger_fraction)
            and not has_retention_pressure
        ):
            return go_to(CallModel, None)

        cutoff = self.flow.compaction_cutoff(context, config, state)
        if cutoff <= state.summarized_through_sequence:
            return go_to(CallModel, None)
        messages = self.flow.load_messages(
            context,
            state.summarized_through_sequence + 1,
            cutoff,
            config,
        )
        previous_summary = self.flow.get_summary(context).content
        summary = await self.flow.model_client.summarize(
            config,
            previous_summary,
            messages,
        )
        generation = state.compaction_generation + 1
        self.flow.summary.set(
            context,
            ContextSummary(generation, cutoff, summary),
        )
        state = replace(
            state,
            summarized_through_sequence=cutoff,
            compaction_generation=generation,
        )
        self.flow.state.set(context, state)
        self.flow.trim_summarized_messages(context, config, state)
        self.flow.events.write(
            context,
            AgentEvent(
                "compaction",
                f"Compacted conversation through message {cutoff}.",
            ),
        )
        return go_to(CallModel, None)


class CallModel(Step[None]):
    def __init__(self, flow: AIAgentFlow) -> None:
        self.flow = flow

    def get_step_options(self) -> StepOptions:
        return MODEL_OPTIONS

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> StepDecision:
        self.flow.update_status(context, STATUS_CALLING_MODEL)
        config = self.flow.config.get(context)
        state = self.flow.state.get(context)

        progress = self.flow.assistant_text.buffered_text(context)

        reply = await self.flow.model_client.complete(
            config,
            self.flow.context_messages(context, config, state),
            self.flow.tool_definitions(config),
            progress.write,
        )
        self.flow.append_message(
            context,
            AgentMessage("assistant", reply.content, reply.tool_calls),
        )
        state = self.flow.state.get(context)
        state = self.flow.trim_summarized_messages(context, config, state)
        if not reply.tool_calls:
            self.flow.update_status(context, STATUS_WAITING)
            return go_to(AwaitUser, None)
        self.flow.state.set(
            context,
            replace(
                state,
                status=STATUS_ROUTING_TOOL,
                pending_tool_calls=reply.tool_calls,
                pending_tool_index=0,
            ),
        )
        return go_to(RouteTool, None)


class RouteTool(Step[None]):
    def __init__(self, flow: AIAgentFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, input: None) -> StepDecision:
        self.flow.update_status(context, STATUS_ROUTING_TOOL)
        call = self.flow.current_tool_call(context)
        config = self.flow.config.get(context)
        try:
            definition = self.flow.tool_definition(config, call.name)
        except ValueError:
            self.flow.append_tool_result(
                context,
                call,
                ToolExecutionResult(
                    json.dumps(
                        {
                            "status": "failed",
                            "error": "unknown_or_disabled_tool",
                            "tool": call.name,
                        },
                        ensure_ascii=False,
                    ),
                    True,
                ),
            )
            return self.flow.advance_tool(context)
        if call.name == "durable_wait":
            arguments = _tool_arguments(call)
            duration_seconds = int(arguments.get("duration_seconds", 0))
            reason = str(arguments.get("reason", "Requested wait"))
            if duration_seconds <= 0:
                self.flow.append_tool_result(
                    context,
                    call,
                    ToolExecutionResult(
                        '{"status":"failed","error":"duration_seconds must be positive"}',
                        True,
                    ),
                )
                return self.flow.advance_tool(context)
            self.flow.pending_timer.set(
                context,
                PendingTimer(call.id, duration_seconds, reason),
            )
            return go_to(DurableWait, None)

        if definition.requires_approval:
            self.flow.pending_approval.set(
                context,
                PendingApproval(call.id, call.name, call.arguments_json),
            )
            return go_to(AwaitToolApproval, None)
        return go_to(ExecuteTool, None)


class AwaitToolApproval(Step[None]):
    def __init__(self, flow: AIAgentFlow) -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: None) -> Wait:
        call = self.flow.current_tool_call(context)
        self.flow.update_status(context, STATUS_WAITING_APPROVAL)
        return Wait.until(self.flow.tool_approvals.for_one(call.id))

    def execute(self, context: Context, input: None) -> StepDecision:
        call = self.flow.current_tool_call(context)
        approvals = self.flow.tool_approvals.results(context, call.id)
        if not approvals:
            raise RuntimeError("the approval wait completed without a decision")
        self.flow.pending_approval.delete(context)
        if approvals[0].approved:
            return go_to(ExecuteTool, None)
        self.flow.append_tool_result(
            context,
            call,
            ToolExecutionResult('{"status":"rejected_by_user"}', True),
        )
        return self.flow.advance_tool(context)


class ExecuteTool(Step[None]):
    def __init__(self, flow: AIAgentFlow) -> None:
        self.flow = flow

    def get_step_options(self) -> StepOptions:
        return TOOL_OPTIONS

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> StepDecision:
        self.flow.update_status(context, STATUS_EXECUTING_TOOL)
        call = self.flow.current_tool_call(context)
        config = self.flow.config.get(context)

        async def write_progress(message: str) -> None:
            self.flow.events.write(
                context,
                AgentEvent("tool_progress", message, call.id, call.name),
            )

        try:
            result = await self.flow.mcp_registry.execute(
                call.name,
                _tool_arguments(call),
                config.enabled_mcp_servers,
                write_progress,
            )
        except Exception as error:
            self.flow.events.write(
                context,
                AgentEvent(
                    "tool_error",
                    f"{call.name} failed with {type(error).__name__}.",
                    call.id,
                    call.name,
                ),
            )
            result = ToolExecutionResult(
                json.dumps(
                    {
                        "status": "failed",
                        "outcome": "known_failure",
                        "error_type": type(error).__name__,
                    },
                    ensure_ascii=False,
                ),
                True,
            )
        self.flow.append_tool_result(context, call, result)
        self.flow.events.write(
            context,
            AgentEvent("tool_result", result.content, call.id, call.name),
        )
        return self.flow.advance_tool(context)


class DurableWait(Step[None]):
    def __init__(self, flow: AIAgentFlow) -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: None) -> Wait:
        timer = self.flow.pending_timer.get(context)
        self.flow.update_status(context, STATUS_WAITING_TIMER)
        return Wait.any_of(
            Timer.by_duration(
                timedelta(seconds=timer.duration_seconds),
                condition_id="durable-wait-timer",
            ),
            self.flow.user_messages.for_one(condition_id="durable-wait-user"),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        call = self.flow.current_tool_call(context)
        timer = self.flow.pending_timer.get(context)
        user_messages = self.flow.user_messages.results(context)
        self.flow.pending_timer.delete(context)
        if user_messages:
            self.flow.append_tool_result(
                context,
                call,
                ToolExecutionResult(
                    json.dumps(
                        {"status": "interrupted", "reason": timer.reason},
                        ensure_ascii=False,
                    ),
                    True,
                ),
            )
            self.flow.append_message(
                context,
                AgentMessage("user", user_messages[0].content),
            )
            state = self.flow.state.get(context)
            self.flow.state.set(
                context,
                replace(state, pending_tool_calls=[], pending_tool_index=0),
            )
            return go_to(CompactContext, None)
        self.flow.append_tool_result(
            context,
            call,
            ToolExecutionResult(
                json.dumps(
                    {
                        "status": "completed",
                        "duration_seconds": timer.duration_seconds,
                        "reason": timer.reason,
                    },
                    ensure_ascii=False,
                ),
                False,
            ),
        )
        return self.flow.advance_tool(context)


class AIAgentFlow(Flow[AgentConfig]):
    config = Attribute("AgentConfig", AgentConfig)
    state = Attribute("AgentState", AgentState)
    summary = Attribute("ContextSummary", ContextSummary)
    messages = AttributeMap("AgentMessages", AgentMessage)
    pending_approval = Attribute("PendingApproval", PendingApproval)
    pending_timer = Attribute("PendingTimer", PendingTimer)
    user_messages = Channel("UserMessages", UserMessage)
    tool_approvals = ChannelMap("ToolApprovals", ToolApproval)
    assistant_text = Stream("AssistantText", str, 10 * 1024 * 1024)
    events = Stream("AgentEvents", AgentEvent, 10 * 1024 * 1024)

    def __init__(
        self,
        model_client: ModelClient,
        mcp_registry: MCPRegistry,
    ) -> None:
        self.model_client = model_client
        self.mcp_registry = mcp_registry
        self.init = Init(self)
        self.await_user = AwaitUser(self)
        self.compact_context = CompactContext(self)
        self.call_model = CallModel(self)
        self.route_tool = RouteTool(self)
        self.await_tool_approval = AwaitToolApproval(self)
        self.execute_tool = ExecuteTool(self)
        self.durable_wait = DurableWait(self)

    def get_steps(self) -> StepList[AgentConfig]:
        return StepList.start_step(self.init).other_steps(
            self.await_user,
            self.compact_context,
            self.call_model,
            self.route_tool,
            self.await_tool_approval,
            self.execute_tool,
            self.durable_wait,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.config,
            self.state,
            self.summary,
            self.messages,
            self.pending_approval,
            self.pending_timer,
            self.user_messages,
            self.tool_approvals,
            self.assistant_text,
            self.events,
        )

    def validate_config(self, config: AgentConfig) -> None:
        unknown_servers = set(config.enabled_mcp_servers) - set(
            self.mcp_registry.server_names
        )
        if unknown_servers:
            raise ValueError(f"unknown MCP servers: {sorted(unknown_servers)}")
        available_tools = {definition.name for definition in self.tool_definitions(config)}
        unknown_tools = set(config.enabled_tools) - available_tools
        if unknown_tools:
            raise ValueError(f"unknown tools: {sorted(unknown_tools)}")

    def tool_definitions(self, config: AgentConfig) -> list[ToolDefinition]:
        definitions = self.mcp_registry.definitions(
            config.enabled_mcp_servers,
            config.enabled_tools,
        )
        definitions.append(_durable_wait_definition())
        return definitions

    def tool_definition(self, config: AgentConfig, name: str) -> ToolDefinition:
        definitions = {
            definition.name: definition for definition in self.tool_definitions(config)
        }
        try:
            return definitions[name]
        except KeyError as error:
            raise ValueError(f"unknown or disabled tool {name!r}") from error

    def append_message(self, context: Context, message: AgentMessage) -> int:
        state = self.state.get(context)
        sequence = state.next_sequence
        self.messages.set(context, _sequence_key(sequence), message)
        self.state.set(
            context,
            replace(state, next_sequence=sequence + 1, last_sequence=sequence),
        )
        return sequence

    def append_tool_result(
        self,
        context: Context,
        call: ToolCall,
        result: ToolExecutionResult,
    ) -> None:
        self.append_message(
            context,
            AgentMessage(
                "tool",
                result.content,
                tool_call_id=call.id,
                tool_name=call.name,
            ),
        )

    def advance_tool(self, context: Context) -> StepDecision:
        state = self.state.get(context)
        next_index = state.pending_tool_index + 1
        if next_index < len(state.pending_tool_calls):
            self.state.set(context, replace(state, pending_tool_index=next_index))
            return go_to(RouteTool, None)
        self.state.set(
            context,
            replace(state, pending_tool_calls=[], pending_tool_index=0),
        )
        return go_to(CompactContext, None)

    def current_tool_call(self, context: Context) -> ToolCall:
        state = self.state.get(context)
        try:
            return state.pending_tool_calls[state.pending_tool_index]
        except IndexError as error:
            raise RuntimeError("the Agent has no pending tool call") from error

    def update_status(self, context: Context, status: str) -> None:
        state = self.state.get(context)
        if state.status != status:
            self.state.set(context, replace(state, status=status))

    def get_summary(self, context: Context) -> ContextSummary:
        try:
            summary = self.summary.get(context)
        except KeyError:
            return ContextSummary(0, 0, "")
        return summary or ContextSummary(0, 0, "")

    def context_messages(
        self,
        context: Context,
        config: AgentConfig,
        state: AgentState,
    ) -> list[AgentMessage]:
        result: list[AgentMessage] = []
        summary = self.get_summary(context)
        if summary.content:
            result.append(
                AgentMessage(
                    "system",
                    f"Conversation summary through message {summary.summarized_through_sequence}:\n{summary.content}",
                )
            )
        start = max(
            state.first_retained_sequence,
            state.summarized_through_sequence + 1,
        )
        result.extend(self.load_messages(context, start, state.last_sequence, config))
        return result

    def load_messages(
        self,
        context: Context,
        start: int,
        end: int,
        config: AgentConfig,
    ) -> list[AgentMessage]:
        if end < start:
            return []
        return [
            _project_message(
                self.messages.get(context, _sequence_key(sequence)),
                config.max_context_tokens,
            )
            for sequence in range(start, end + 1)
        ]

    def compaction_cutoff(
        self,
        context: Context,
        config: AgentConfig,
        state: AgentState,
    ) -> int:
        start = max(
            state.first_retained_sequence,
            state.summarized_through_sequence + 1,
        )
        if start >= state.last_sequence:
            return state.summarized_through_sequence
        keep_tokens = max(
            1,
            int(config.max_context_tokens * config.compaction_keep_fraction),
        )
        retained_tokens = 0
        cutoff = state.last_sequence - 1
        for sequence in range(state.last_sequence, start - 1, -1):
            message = _project_message(
                self.messages.get(context, _sequence_key(sequence)),
                config.max_context_tokens,
            )
            retained_tokens += self.model_client.count_tokens(config.model, [message])
            if retained_tokens > keep_tokens:
                cutoff = sequence
                break
            cutoff = sequence - 1
        return max(state.summarized_through_sequence, cutoff)

    def trim_summarized_messages(
        self,
        context: Context,
        config: AgentConfig,
        state: AgentState,
    ) -> AgentState:
        retained = max(0, state.last_sequence - state.first_retained_sequence + 1)
        first = state.first_retained_sequence
        while (
            retained > config.message_retention_limit
            and first <= state.summarized_through_sequence
        ):
            self.messages.delete(context, _sequence_key(first))
            first += 1
            retained -= 1
        if first != state.first_retained_sequence:
            state = replace(state, first_retained_sequence=first)
            self.state.set(context, state)
        return state

    @rpc
    def send_message(self, context: Context, input: UserMessage) -> RPCResult[bool]:
        if not input.content.strip():
            return RPCResult(False)
        self.user_messages.publish(context, input)
        return RPCResult(True)

    @rpc
    def approve_tool(
        self,
        context: Context,
        input: ToolApprovalRequest,
    ) -> RPCResult[bool]:
        try:
            pending = self.pending_approval.get(context)
        except KeyError:
            return RPCResult(False)
        if pending.call_id != input.call_id:
            return RPCResult(False)
        self.tool_approvals.publish(
            context,
            input.call_id,
            ToolApproval(input.approved),
        )
        return RPCResult(True)

    @rpc
    def history(self, context: Context, input: HistoryRequest) -> RPCResult[HistoryPage]:
        state = self.state.get(context)
        if state is None:
            return RPCResult(HistoryPage([], None))
        limit = max(1, min(input.limit, 200))
        end = min(
            input.before_sequence or state.last_sequence + 1,
            state.last_sequence + 1,
        )
        start = max(state.first_retained_sequence, end - limit)
        messages = [
            SequencedMessage(
                sequence,
                self.messages.get(context, _sequence_key(sequence)),
            )
            for sequence in range(start, end)
        ]
        next_before = start if start > state.first_retained_sequence else None
        return RPCResult(HistoryPage(messages, next_before))

    @rpc
    def describe(self, context: Context) -> RPCResult[AgentDescription]:
        state = self.state.get(context)
        config = self.config.get(context)
        if state is None or config is None:
            return RPCResult(
                AgentDescription(
                    status="initializing",
                    model="",
                    system_prompt="",
                    first_retained_sequence=1,
                    last_sequence=0,
                    summarized_through_sequence=0,
                    pending_approval_call_id=None,
                    pending_approval_tool_name=None,
                    pending_approval_arguments_json=None,
                    pending_timer_call_id=None,
                    pending_timer_duration_seconds=None,
                    pending_timer_reason=None,
                    available_mcp_servers=self.mcp_registry.server_names,
                    available_tools=["durable_wait"],
                )
            )
        approval = _optional_attribute(self.pending_approval, context)
        timer = _optional_attribute(self.pending_timer, context)
        return RPCResult(
            AgentDescription(
                status=state.status,
                model=config.model,
                system_prompt=config.system_prompt,
                first_retained_sequence=state.first_retained_sequence,
                last_sequence=state.last_sequence,
                summarized_through_sequence=state.summarized_through_sequence,
                pending_approval_call_id=(approval.call_id if approval else None),
                pending_approval_tool_name=(approval.tool_name if approval else None),
                pending_approval_arguments_json=(
                    approval.arguments_json if approval else None
                ),
                pending_timer_call_id=(timer.call_id if timer else None),
                pending_timer_duration_seconds=(
                    timer.duration_seconds if timer else None
                ),
                pending_timer_reason=(timer.reason if timer else None),
                available_mcp_servers=self.mcp_registry.server_names,
                available_tools=[
                    definition.name for definition in self.tool_definitions(config)
                ],
            )
        )


def _durable_wait_definition() -> ToolDefinition:
    return ToolDefinition(
        name="durable_wait",
        description=(
            "Wait durably before continuing. A new user message interrupts the wait."
        ),
        input_schema={
            "type": "object",
            "properties": {
                "duration_seconds": {"type": "integer", "minimum": 1},
                "reason": {"type": "string"},
            },
            "required": ["duration_seconds", "reason"],
        },
        requires_approval=False,
        timeout_seconds=0,
        maximum_attempts=1,
        retry_total_seconds=0,
    )


def _tool_arguments(call: ToolCall) -> dict[str, Any]:
    try:
        arguments = json.loads(call.arguments_json)
    except json.JSONDecodeError as error:
        raise ValueError(f"tool {call.name!r} has invalid JSON arguments") from error
    if not isinstance(arguments, dict):
        raise ValueError(f"tool {call.name!r} arguments must be an object")
    return arguments


def _sequence_key(sequence: int) -> str:
    return f"{sequence:020d}"


def _project_message(message: AgentMessage, max_context_tokens: int) -> AgentMessage:
    max_characters = max(1_000, max_context_tokens * 4 // 5)
    if len(message.content) <= max_characters:
        return message
    suffix = "\n[Content truncated in the model context; the durable message is complete.]"
    return replace(message, content=message.content[:max_characters] + suffix)


def _optional_attribute(
    attribute: Attribute[Any],
    context: Context,
) -> Any | None:
    try:
        return attribute.get(context)
    except KeyError:
        return None
