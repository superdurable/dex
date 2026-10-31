# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Run a Pydantic AI agent as a durable Dex agent session in Step mode."""

from __future__ import annotations

import json
import types
import typing
from collections.abc import Callable, Mapping, Sequence
from datetime import timedelta
from typing import Any, Generic, TypeVar

from pydantic_ai import (
    AgentRunResult,
    DeferredToolRequests,
    DeferredToolResults,
    ModelMessage,
    ModelRequest,
    ModelResponse,
    RunContext,
    RunUsage,
    ToolApproved,
    ToolCallPart,
    ToolDenied,
    ToolFailed,
    ToolReturn,
    ToolReturnPart,
    UnexpectedModelBehavior,
)
from pydantic_ai.agent import AbstractAgent, WrapperAgent
from pydantic_ai.capabilities import ReinjectSystemPrompt
from pydantic_ai.messages import UserContent
from pydantic_ai.models import KnownModelName, Model
from pydantic_ai.output import OutputSpec

from dex_agents import (
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    AgentFrameworkRunner,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentToolCall,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    DeferredToolCall,
    DeferredToolExecution,
    FrameworkMessage,
    ToolCallsRequested,
    ToolCallStatus,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
    describe_tool_call_outcome,
)
from dex_agents_pydantic_ai._capability import DexModelStepCapability, DexToolStepCapability
from dex_agents_pydantic_ai._history import (
    build_turn_messages,
    build_user_prompt_message,
    read_turn_user_prompt,
    rebuild_earlier_turn_history,
)
from dex_agents_pydantic_ai._run_state import PendingToolCallsState

AgentDepsT = TypeVar("AgentDepsT")

_REINJECT_SYSTEM_PROMPT_CAPABILITY_ID = "dex_agents_reinject_system_prompt"

#: Run metadata key that carries the idempotency key into a tool's ``RunContext``.
IDEMPOTENCY_KEY_METADATA_KEY = "dex_idempotency_key"


def deferred_tool_idempotency_key(ctx: RunContext[Any]) -> str | None:
    """Return the Dex idempotency key of the tool call that is running, if any.

    Every tool call runs in its own Dex tool Step. There the adapter puts the
    call's idempotency key in the run metadata. It is exactly
    ``DeferredToolExecution.idempotency_key``:
    ``"<session ID>/<turn ID>/<tool call ID>"``. Pass it to the external
    system when that system deduplicates requests, so an ``IDEMPOTENT_WRITE``
    retry after a crash has no second effect. The key is the same on every
    Step attempt and after Worker restarts.

    Example:
        >>> @agent.tool
        ... async def create_refund(ctx: RunContext[Payments], order_id: str) -> str:
        ...     key = deferred_tool_idempotency_key(ctx)
        ...     refund = await ctx.deps.refunds.create(order_id, idempotency_key=key)
        ...     return f"refund {refund.id} created"

    Args:
        ctx: The ``RunContext`` Pydantic AI passes to the tool.

    Returns:
        The idempotency key when the tool runs in a Dex tool Step, or ``None``
        when the agent runs outside a Dex session.
    """
    key = (ctx.metadata or {}).get(IDEMPOTENCY_KEY_METADATA_KEY)
    return key if isinstance(key, str) else None


class PydanticAIRunner(AgentFrameworkRunner, Generic[AgentDepsT]):
    """Run one Pydantic AI agent inside the durable Dex agent session Flow, in Step mode.

    The runner implements ``AgentFrameworkRunner`` for Pydantic AI. Most
    applications call ``pydantic_ai_session_flow`` instead of constructing it.
    Construct it yourself to pass it to ``AgentSessionFlow`` with options you
    build separately.

    The agent's own code stays unchanged. Each ``RunModel`` Step runs the agent
    with a Pydantic AI capability that makes every function tool an external
    tool. The model call that asks for tools therefore ends the run with
    ``DeferredToolRequests`` before any tool runs, and the runner returns
    ``ToolCallsRequested`` whose run state is the turn's messages so far. Each
    tool call then runs in its own Dex Step, in parallel. When every call has a
    final record, the next ``RunModel`` resumes the run with
    ``DeferredToolResults`` built from those records.

    ``execute_deferred_tool_call`` runs the original tool once, through the
    agent itself: argument validation, the agent's capabilities and hooks,
    dependencies, and toolset lifecycles such as MCP connections all apply.
    The model is not called in a tool Step.

    Example:
        >>> runner = PydanticAIRunner(support_agent, deps_factory=lambda session_id: deps)
        >>> flow = AgentSessionFlow(runner, flow_type="SupportAgent",
        ...                         options=AgentSessionOptions(tool_policies=policies))

    Type Parameters:
        AgentDepsT: The agent's dependencies type.
    """

    def __init__(
        self,
        agent: AbstractAgent[AgentDepsT, Any],
        *,
        deps_factory: Callable[[str], AgentDepsT] | None = None,
        model: Model | KnownModelName | str | None = None,
    ) -> None:
        """Create a runner for one agent.

        Construct it once at Worker startup. The runner keeps no state between
        calls, so one runner serves every session of its Flow type, and tool
        Steps may call it concurrently.

        Args:
            agent: The Pydantic AI agent, or a ``WrapperAgent`` around one. Its
                ``output_type`` may already list ``DeferredToolRequests``;
                otherwise each run adds it.
            deps_factory: Builds the agent's dependencies from the session ID.
                The runner calls it on the Worker before every model Step and
                every tool Step, so it may run many times per session, in
                parallel, and again after a crash. ``None`` passes ``None`` as
                deps.
            model: Model for every run, overriding the agent's default model.
                ``None`` uses the agent's model.

        Raises:
            ValueError: If the agent has output validators and its
                ``output_type`` does not list ``DeferredToolRequests``. Pydantic AI
                refuses a run-level ``output_type`` for such an agent; list
                ``DeferredToolRequests`` in the agent's ``output_type`` instead.
        """
        self._agent = agent
        self._deps_factory = deps_factory
        self._model = model
        self._run_output_type = _run_output_type_with_deferred_tool_requests(agent)

    @property
    def agent(self) -> AbstractAgent[AgentDepsT, Any]:
        """Return the agent this runner runs.

        Returns:
            The agent passed to the constructor.
        """
        return self._agent

    def build_user_message(self, text: str) -> FrameworkMessage:
        """Store user text as a Pydantic AI ``ModelRequest`` with one ``UserPromptPart``.

        The session also stores the turn-failure note this way.

        Args:
            text: The user message text.

        Returns:
            A user message whose ``framework_item`` holds the request.
        """
        return build_user_prompt_message(text)

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        """Let the agent make the turn's next model call.

        The runner replays the session's earlier turns as ``message_history``.
        The turn's first model call runs its user prompt; a later one continues
        from the stored run state with the recorded tool results.

        Args:
            turn: Committed session state for this model call.

        Returns:
            ``ToolCallsRequested`` when the model asked for tools, otherwise
            ``TurnCompleted`` with every message the turn produced.

        Raises:
            Exception: Any model or Pydantic AI error fails the attempt.
            RuntimeError: Pydantic AI answered a tool call the session never
                ran, such as one dropped beside a final result.
        """
        prior_history = rebuild_earlier_turn_history(turn.messages, turn.turn_id)
        user_prompt = read_turn_user_prompt(turn.messages, turn.turn_id)
        capability = DexModelStepCapability(turn=turn)
        earlier_turn_messages: Sequence[ModelMessage] = ()
        recorded_tool_call_ids: Sequence[str] = ()
        if turn.framework_run_state is None:
            result = await self._run_model_call(turn, capability, user_prompt, prior_history, None)
        else:
            pending = PendingToolCallsState.from_json(turn.framework_run_state)
            earlier_turn_messages = pending.turn_messages
            recorded_tool_call_ids = pending.tool_call_ids
            result = await self._run_model_call(
                turn,
                capability,
                None,
                [*prior_history, *earlier_turn_messages],
                _recorded_deferred_tool_results(turn, pending),
            )
        new_messages = result.new_messages()
        turn_messages = [*earlier_turn_messages, *new_messages]
        usage = _agent_usage(result.usage)
        if isinstance(result.output, DeferredToolRequests):
            tool_calls = _requested_tool_calls(result.output, turn_messages, capability)
            return ToolCallsRequested(
                framework_run_state=PendingToolCallsState(
                    turn_messages=turn_messages,
                    tool_call_ids=[call.tool_call_id for call in tool_calls],
                    structured_result_tool_call_ids=[
                        call.tool_call_id
                        for call in tool_calls
                        if capability.has_structured_result(call.tool_name)
                    ],
                ).to_json(),
                tool_calls=tool_calls,
                usage=usage,
            )
        _require_every_tool_result_recorded(new_messages, capability, recorded_tool_call_ids)
        return TurnCompleted(
            new_messages=build_turn_messages(turn_messages, user_prompt), usage=usage
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        """Run one tool call through the agent, without a model call.

        The runner starts an agent run whose history holds only the tool call
        and stops it once the tool returns. An approved call runs with
        ``RunContext.tool_call_approved`` set; a call that needed no approval
        runs without it. The run metadata carries the idempotency key, which
        ``deferred_tool_idempotency_key`` reads.

        Args:
            call: The approved record and its idempotency key.

        Returns:
            The tool's return value as the text the model sees.

        Raises:
            ToolExecutionError: The tool raised ``ModelRetry`` or ``ToolFailed``,
                ran out of its own retries, is unknown or external, or asked for
                approval while it ran. The model sees the message.
            RuntimeError: The tool raised ``CallDeferred`` while it ran, so its
                work may have taken effect.
            UnexpectedModelBehavior: The tool body raised it, such as from a
                sub-agent run, so its work may have taken effect.
            Exception: Any other tool error fails the Step attempt. After the
                last attempt the session records ``FAILED`` for a ``READ`` call
                and ``OUTCOME_UNKNOWN`` for a write.
        """
        record = call.tool_call
        tool_call = ToolCallPart(record.tool_name, record.arguments, record.tool_call_id)
        approval = (
            DeferredToolResults(approvals={record.tool_call_id: ToolApproved()})
            if record.requires_approval
            else None
        )
        capability = DexToolStepCapability(tool_name=record.tool_name)
        try:
            async with self._agent.iter(
                None,
                output_type=self._run_output_type,
                message_history=[ModelResponse(parts=[tool_call])],
                deferred_tool_results=approval,
                deps=self._create_deps(call.flow_id),
                model=self._model,
                conversation_id=call.flow_id,
                metadata={IDEMPOTENCY_KEY_METADATA_KEY: call.idempotency_key},
                capabilities=[capability],
            ) as agent_run:
                node = agent_run.next_node
                while not AbstractAgent.is_model_request_node(node):
                    if AbstractAgent.is_end_node(node):
                        raise _tool_deferred_again_error(record, node.data.output, capability)
                    node = await agent_run.next(node)
                tool_return = next(
                    (
                        part
                        for part in node.request.parts
                        if isinstance(part, ToolReturnPart)
                        and part.tool_call_id == record.tool_call_id
                    ),
                    None,
                )
        except UnexpectedModelBehavior as error:
            if not capability.is_tool_retry_limit_error(error):
                raise
            raise ToolExecutionError(str(error)) from error
        if tool_return is None:
            raise RuntimeError(f"tool call {record.tool_call_id} produced no tool return")
        if tool_return.outcome != "success":
            raise ToolExecutionError(tool_return.model_response_str(wrap_if_error=False))
        return ToolExecutionResult(output=tool_return.model_response_str())

    async def _run_model_call(
        self,
        turn: AgentTurnContext,
        capability: DexModelStepCapability,
        user_prompt: str | Sequence[UserContent] | None,
        message_history: Sequence[ModelMessage],
        deferred_tool_results: DeferredToolResults | None,
    ) -> AgentRunResult[Any]:
        return await self._agent.run(
            user_prompt,
            output_type=self._run_output_type,
            message_history=message_history,
            deferred_tool_results=deferred_tool_results,
            deps=self._create_deps(turn.flow_id),
            model=self._model,
            conversation_id=turn.flow_id,
            # Restores system prompts when no replayed turn holds them, as after failed first turns.
            capabilities=[
                capability,
                ReinjectSystemPrompt(id=_REINJECT_SYSTEM_PROMPT_CAPABILITY_ID),
            ],
        )

    def _create_deps(self, session_id: str) -> Any:
        return None if self._deps_factory is None else self._deps_factory(session_id)


def pydantic_ai_session_flow(
    agent: AbstractAgent[AgentDepsT, Any],
    *,
    flow_type: str,
    tool_policies: Mapping[str, ToolPolicy] | None = None,
    default_tool_effect: ToolEffect = ToolEffect.UNCLASSIFIED,
    interrupt_deadline: timedelta | None = None,
    event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    deps_factory: Callable[[str], AgentDepsT] | None = None,
    model: Model | KnownModelName | str | None = None,
) -> AgentSessionFlow:
    """Build the durable agent session Flow for one Pydantic AI agent.

    One Flow execution is one agent session, and the Flow ID is the session
    ID. Register the returned Flow in a Registry created with
    ``allow_async_handlers=True``, serve it with an ``AsyncWorker``, and drive
    sessions with ``AgentSessionClient``. Build the Flow once per agent at
    startup, the same way in the Worker and in every Client process.

    The session runs in Step mode: each model call is one ``RunModel`` Step,
    and each tool call is its own Step, run in parallel with the other calls of
    the same model response. A tool's policy picks its Step and whether it
    waits for approval. Without a policy, a tool is ``default_tool_effect``,
    ``UNCLASSIFIED`` by default: it runs once, after a person approves it. MCP
    tools that declare ``readOnlyHint`` are ``READ``, those that declare
    ``idempotentHint`` are ``IDEMPOTENT_WRITE``, and Pydantic AI's tool search
    is ``READ``. A tool declared with Pydantic AI's ``requires_approval=True``
    always waits for approval.

    Example:
        >>> agent = Agent("openai:gpt-5.2", instructions="Help with orders.")
        >>> @agent.tool_plain
        ... def find_order(order_id: str) -> str: ...
        >>> @agent.tool_plain
        ... def cancel_order(order_id: str) -> str: ...
        >>> flow = pydantic_ai_session_flow(
        ...     agent,
        ...     flow_type="OrderAgent",
        ...     tool_policies={"find_order": ToolPolicy(ToolEffect.READ)},
        ... )
        >>> registry = Registry([flow], allow_async_handlers=True)

    Args:
        agent: The Pydantic AI agent, or a ``WrapperAgent`` around one. Its code
            stays unchanged.
        flow_type: Protocol Flow type, unique within the Registry. Keep it
            stable: running sessions resume by it.
        tool_policies: Policies by tool name, as the model calls the tool
            (including any toolset prefix). They override MCP annotations and
            ``default_tool_effect``. ``None`` means no policies.
        default_tool_effect: Effect of a tool without a policy or MCP
            annotation. Defaults to ``UNCLASSIFIED``.
        interrupt_deadline: How long an approval may wait before the session
            rejects the call with ``"approval deadline passed"``. ``None``, the
            default, waits indefinitely.
        event_stream_capacity_bytes: Byte budget of the ``AgentEvents`` Stream,
            shared by every session of this Flow type. Defaults to 10 MiB.
        deps_factory: Builds the agent's dependencies from the session ID on
            the Worker, before every model Step and tool Step. ``None`` passes
            ``None``.
        model: Model for every run instead of the agent's default model.

    Returns:
        An ``AgentSessionFlow`` backed by a ``PydanticAIRunner``.

    Raises:
        ValueError: If ``flow_type`` is blank, an option is not positive, or
            the agent has output validators without ``DeferredToolRequests`` in
            its ``output_type``.
    """
    return AgentSessionFlow(
        PydanticAIRunner(agent, deps_factory=deps_factory, model=model),
        flow_type=flow_type,
        options=AgentSessionOptions(
            tool_policies=tool_policies or {},
            default_tool_effect=default_tool_effect,
            event_stream_capacity_bytes=event_stream_capacity_bytes,
            interrupt_deadline=interrupt_deadline,
        ),
    )


def _run_output_type_with_deferred_tool_requests(
    agent: AbstractAgent[Any, Any],
) -> OutputSpec[Any] | None:
    if _output_spec_lists_deferred_tool_requests(agent.output_type):
        return None
    wrapped_agent = agent
    while isinstance(wrapped_agent, WrapperAgent):
        wrapped_agent = wrapped_agent.wrapped
    # Pydantic AI exposes no public accessor for output validators.
    if getattr(wrapped_agent, "_output_validators", None):
        raise ValueError(
            "the agent has output validators, so list DeferredToolRequests in its output_type, "
            "such as output_type=[MyOutput, DeferredToolRequests]"
        )
    return [agent.output_type, DeferredToolRequests]


def _output_spec_lists_deferred_tool_requests(output_spec: object) -> bool:
    if output_spec is DeferredToolRequests:
        return True
    if isinstance(output_spec, Sequence) and not isinstance(output_spec, str | bytes):
        return any(_output_spec_lists_deferred_tool_requests(item) for item in output_spec)
    if typing.get_origin(output_spec) in (typing.Union, types.UnionType):
        return any(
            _output_spec_lists_deferred_tool_requests(item) for item in typing.get_args(output_spec)
        )
    return False


def _recorded_deferred_tool_results(
    turn: AgentTurnContext,
    pending: PendingToolCallsState,
) -> DeferredToolResults:
    results = DeferredToolResults()
    structured_result_ids = set(pending.structured_result_tool_call_ids)
    for tool_call_id in pending.tool_call_ids:
        record = turn.recorded_tool_call(tool_call_id)
        if record is None:
            raise RuntimeError(f"turn {turn.turn_id} has no record for tool call {tool_call_id}")
        outcome = describe_tool_call_outcome(record)
        if record.status is ToolCallStatus.SUCCEEDED:
            results.calls[tool_call_id] = ToolReturn(
                _parse_structured_output(outcome)
                if tool_call_id in structured_result_ids
                else outcome
            )
        elif record.status is ToolCallStatus.REJECTED:
            results.approvals[tool_call_id] = ToolDenied(outcome)
        else:
            results.calls[tool_call_id] = ToolFailed(outcome)
    return results


def _parse_structured_output(output: str) -> Any:
    # Typed tool parts, such as tool search results, need their structured value back.
    try:
        return json.loads(output)
    except ValueError:
        return output


def _requested_tool_calls(
    requests: DeferredToolRequests,
    turn_messages: Sequence[ModelMessage],
    capability: DexModelStepCapability,
) -> list[DeferredToolCall]:
    response = next(
        message for message in reversed(turn_messages) if isinstance(message, ModelResponse)
    )
    response_order = {part.tool_call_id: index for index, part in enumerate(response.tool_calls)}
    approval_ids = {part.tool_call_id for part in requests.approvals}
    requested_parts = sorted(
        [*requests.approvals, *requests.calls],
        key=lambda part: response_order.get(part.tool_call_id, len(response_order)),
    )
    tool_calls: list[DeferredToolCall] = []
    for part in requested_parts:
        policy = capability.resolve_tool_policy(part.tool_name)
        # Pydantic AI asked for approval, such as for requires_approval=True, so Dex asks too.
        if part.tool_call_id in approval_ids and not policy.is_approval_required:
            policy = ToolPolicy(policy.effect, requires_approval=True)
        tool_calls.append(
            DeferredToolCall(
                tool_call_id=part.tool_call_id,
                tool_name=part.tool_name,
                arguments=part.args_as_json_str(),
                policy=policy,
            )
        )
    return tool_calls


def _require_every_tool_result_recorded(
    new_messages: Sequence[ModelMessage],
    capability: DexModelStepCapability,
    recorded_tool_call_ids: Sequence[str],
) -> None:
    recorded_ids = set(recorded_tool_call_ids)
    for message in new_messages:
        if not isinstance(message, ModelRequest):
            continue
        for part in message.parts:
            if (
                isinstance(part, ToolReturnPart)
                and part.outcome == "success"
                and part.tool_name in capability.tool_definitions
                and part.tool_call_id not in recorded_ids
            ):
                raise RuntimeError(
                    f"Pydantic AI answered tool call {part.tool_call_id} ({part.tool_name}) "
                    "without running it in a Dex tool Step"
                )


def _tool_deferred_again_error(
    record: AgentToolCall,
    output: object,
    capability: DexToolStepCapability,
) -> Exception:
    if record.tool_call_id in capability.refused_external_tool_call_ids:
        return ToolExecutionError(
            f"tool {record.tool_name} is an external tool and has no function to run in a "
            "Dex tool Step"
        )
    if isinstance(output, DeferredToolRequests) and any(
        part.tool_call_id == record.tool_call_id for part in output.approvals
    ):
        return ToolExecutionError(
            f"tool {record.tool_name} asked for approval while it ran; give it a ToolPolicy "
            "with requires_approval=True"
        )
    return RuntimeError(
        f"tool {record.tool_name} raised CallDeferred in its Dex tool Step, so its work may "
        "have taken effect"
    )


def _agent_usage(usage: RunUsage) -> AgentUsage:
    return AgentUsage(
        model_calls=usage.requests,
        input_tokens=usage.input_tokens,
        output_tokens=usage.output_tokens,
        cached_input_tokens=usage.cache_read_tokens,
        reasoning_tokens=usage.output_reasoning_tokens,
    )
