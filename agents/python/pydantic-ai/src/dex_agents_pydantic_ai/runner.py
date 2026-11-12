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
    Agent,
    AgentRunResult,
    DeferredToolRequests,
    DeferredToolResults,
    ModelMessage,
    ModelRequest,
    ModelResponse,
    RunContext,
    RunUsage,
    Tool,
    ToolApproved,
    ToolCallPart,
    ToolDefinition,
    ToolDenied,
    ToolFailed,
    ToolReturn,
    ToolReturnPart,
    UnexpectedModelBehavior,
    UserPromptPart,
)
from pydantic_ai.agent import AbstractAgent, WrapperAgent
from pydantic_ai.capabilities import ReinjectSystemPrompt
from pydantic_ai.messages import UserContent
from pydantic_ai.models import KnownModelName, Model
from pydantic_ai.output import OutputSpec
from pydantic_ai.toolsets import AbstractToolset, ExternalToolset, FunctionToolset

from dex_agents import (
    CONTEXT_SUMMARY_INSTRUCTIONS,
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    DEFAULT_RETAINED_MESSAGE_LIMIT,
    AgentFrameworkRunner,
    AgentImageReference,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentToolCall,
    AgentTracingOptions,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    CompactionPolicy,
    ContextSummaryRequest,
    ContextSummaryResult,
    DeferredToolCall,
    DeferredToolExecution,
    DeferredToolOutcome,
    FrameworkMessage,
    MediaStore,
    ModelCallMetadata,
    ToolCallsRequested,
    ToolCallStatus,
    ToolCallStepOptions,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
    build_context_summary_note,
    build_context_summary_prompt,
    describe_tool_call_outcome,
    estimate_stored_input_tokens,
    estimate_text_tokens,
    select_turn_boundary_cutoff,
)
from dex_agents.builtin_loop import (
    DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
    FrameworkToolSourceRouter,
    SourceToolListing,
    ToolSource,
)
from dex_agents_pydantic_ai._capability import DexModelStepCapability, DexToolStepCapability
from dex_agents_pydantic_ai._history import (
    build_turn_messages,
    build_user_prompt_message,
    build_user_prompt_message_with_images,
    read_turn_user_prompt,
    rebuild_earlier_turn_history,
)
from dex_agents_pydantic_ai._images import image_reference_placeholder, upload_tool_return_images
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


class PydanticAIRunner(AgentFrameworkRunner[DeferredToolOutcome], Generic[AgentDepsT]):
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

    Tool sources, such as ``ConnectorToolSource``, add tools that the runner
    lists in every ``RunModel`` and offers as external tools of that run, with
    the source's name, description, and JSON Schema. Pydantic AI never runs
    an external tool, so every call of a source tool, reads included, ends
    the run and runs once in its own tool Step through the source that built
    it. The source picks the effect class, the approval, the write ordering,
    and the tool Step's timeout and retry policy. The runner polls no tool
    tasks, so a source call that starts one records ``FAILED`` for a ``READ``
    call and ``OUTCOME_UNKNOWN`` otherwise.

    The runner implements ``ContextCompactionRunner``. Set
    ``AgentSessionOptions.compaction`` to compact at turn boundaries: the
    summary enters as the first ``ModelRequest`` of ``message_history``. Without
    a policy nothing changes.

    With a ``media_store``, the runner also implements
    ``ImageUserMessageBuilder``, so user messages and tool results may carry
    images. See the ``media_store`` argument of the constructor.

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
        summary_model: Model | KnownModelName | str | None = None,
        tool_sources: Sequence[ToolSource] = (),
        tool_source_listing_timeout: timedelta = DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
        media_store: MediaStore | None = None,
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
            summary_model: Model of the ``CompactContext`` summary call.
                ``None`` uses ``model``, else the agent's model.

                .. versionadded:: 0.3.0
            tool_sources: Tool sources, such as ``ConnectorToolSource``, whose
                tools every ``RunModel`` lists and offers as external tools.
                Each source has a unique ``source_name``, which the runner
                stores with every call it builds, so the tool Step reaches
                that source on any Worker. A ``tool_policies`` entry for a
                connector tool changes only its approval. Defaults to none.

                .. versionadded:: 0.4.0
            tool_source_listing_timeout: How long one ``RunModel`` waits for
                each source's ``list_tools``. A source that fails or takes
                longer offers no tools in that model call. Defaults to 10
                seconds.

                .. versionadded:: 0.4.0
            media_store: The store that holds the session's images, such as
                ``S3MediaStore``. With a store, the runner has
                ``build_user_message_with_images``, so ``send_user_message``
                accepts images. A user message is stored as one
                ``UserPromptPart`` with the text and one ``ImageUrl``
                placeholder per image. The placeholder's ``url`` is the
                reference's ``uri``, and its ``vendor_metadata`` holds the
                reference under ``"dex_image_reference"``. A tool call record's
                images go into its ``ToolReturnPart`` content after the output
                text, as the same placeholders. Stored messages and run state
                never hold bytes. Right before each model request, the runner
                reads the bytes with ``load_images`` and replaces each
                placeholder with a ``BinaryContent`` of the reference's MIME
                type. A failed read fails the model call, which retries. A tool
                of the agent that returns image ``BinaryContent`` has the image
                uploaded in its tool Step and recorded as a reference. ``None``,
                the default, keeps the runner without image support, so the
                session refuses images.

                .. versionadded:: 0.5.0

        Raises:
            ValueError: If the agent has output validators and its
                ``output_type`` does not list ``DeferredToolRequests``. Pydantic AI
                refuses a run-level ``output_type`` for such an agent; list
                ``DeferredToolRequests`` in the agent's ``output_type`` instead.
                Also if two tool sources share a ``source_name``, a source's
                ``declared_tool_names`` repeat a function tool of the agent or
                another source's tool, or ``tool_source_listing_timeout`` is not
                positive.
            TypeError: If a tool source lacks a ``ToolSource`` method or a
                non-blank ``source_name``.
        """
        self._agent = agent
        self._deps_factory = deps_factory
        self._model = model
        self._summary_model = summary_model
        self._run_output_type = _run_output_type_with_deferred_tool_requests(agent)
        self._tool_sources = FrameworkToolSourceRouter(
            tool_sources,
            reserved_tool_names=_function_tool_names(agent),
            listing_timeout=tool_source_listing_timeout,
        )
        self._media_store = media_store
        if media_store is not None:
            # The session accepts images only from a runner that has this method.
            self.build_user_message_with_images = self._build_user_message_with_images

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

    def _build_user_message_with_images(
        self, text: str, images: Sequence[AgentImageReference]
    ) -> FrameworkMessage:
        """Store user text and images as one ``UserPromptPart`` with image placeholders.

        It is ``build_user_message_with_images`` of a runner built with a
        ``media_store``. The part's content is the text, then one ``ImageUrl``
        per image, in order, whose ``url`` is the reference's ``uri`` and whose
        ``vendor_metadata`` holds the reference. Before each model request the
        runner replaces them with the bytes.

        .. versionadded:: 0.5.0

        Args:
            text: The user message text.
            images: The message's images, at least one.

        Returns:
            A user message whose ``framework_item`` holds the request.
        """
        return build_user_prompt_message_with_images(text, images)

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
            ValueError: A tool source listed a name that another source
                lists. Pydantic AI raises ``UserError`` for a name that one
                of the agent's own toolsets also has.
        """
        prior_history = rebuild_earlier_turn_history(turn.messages, turn.turn_id)
        if turn.context_summary is not None:
            note = build_context_summary_note(turn.context_summary)
            prior_history.insert(0, ModelRequest(parts=[UserPromptPart(note)]))
        user_prompt = read_turn_user_prompt(turn.messages, turn.turn_id)
        source_tools = await self._tool_sources.list_source_tools(turn)
        capability = DexModelStepCapability(
            turn=turn,
            replay_only_tool_names=frozenset(tool.name for tool in source_tools.replay_only_tools),
            media_store=self._media_store,
        )
        earlier_turn_messages: Sequence[ModelMessage] = ()
        recorded_tool_call_ids: Sequence[str] = ()
        if turn.framework_run_state is None:
            result = await self._run_model_call(
                turn, capability, source_tools, user_prompt, prior_history, None
            )
        else:
            pending = PendingToolCallsState.from_json(turn.framework_run_state)
            earlier_turn_messages = pending.turn_messages
            recorded_tool_call_ids = pending.tool_call_ids
            result = await self._run_model_call(
                turn,
                capability,
                source_tools,
                None,
                [*prior_history, *earlier_turn_messages],
                _recorded_deferred_tool_results(turn, pending),
            )
        new_messages = result.new_messages()
        turn_messages = [*earlier_turn_messages, *new_messages]
        usage = _agent_usage(result.usage)
        if isinstance(result.output, DeferredToolRequests):
            tool_calls = _requested_tool_calls(
                turn, result.output, turn_messages, capability, source_tools
            )
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
                model_call_metadata=capability.model_call_metadata(),
            )
        _require_every_tool_result_recorded(new_messages, capability, recorded_tool_call_ids)
        return TurnCompleted(
            new_messages=build_turn_messages(turn_messages, user_prompt),
            usage=usage,
            model_call_metadata=capability.model_call_metadata(),
        )

    @property
    def summary_model(self) -> Model | KnownModelName | str | None:
        """Return the model that writes a ``ContextSummary``.

        .. versionadded:: 0.3.0

        Returns:
            ``summary_model``, else ``model``, else the agent's model; ``None``
            when none is set, so compaction cannot run.
        """
        return self._summary_model or self._model or self._agent.model

    def estimate_input_tokens(self, turn: AgentTurnContext) -> int:
        """Estimate the next model input from the stored messages and the agent's prompt.

        Pydantic AI builds the request, so this counts every stored message as
        stored, the summary, the run state of a resumed call, and the agent's
        static system prompts, instructions, and function tool definitions.
        Dynamic instructions and tools of other toolsets, such as MCP servers,
        are not visible here; cover them with
        ``CompactionPolicy.reserved_input_tokens``.

        .. versionadded:: 0.3.0

        Args:
            turn: The context of the next model call.

        Returns:
            The estimated input tokens, from ``estimate_text_tokens``.
        """
        return estimate_stored_input_tokens(
            turn, fixed_input_tokens=_estimate_agent_prompt_tokens(self._agent)
        )

    def select_compaction_cutoff(
        self, turn: AgentTurnContext, retained_token_budget: int
    ) -> int | None:
        """Choose the last message to summarize, at a boundary between turns.

        .. versionadded:: 0.3.0

        Args:
            turn: The context of the model call that crossed the threshold.
            retained_token_budget: Tokens that the kept messages may use.

        Returns:
            What ``select_turn_boundary_cutoff`` returns.
        """
        return select_turn_boundary_cutoff(turn, retained_token_budget)

    async def summarize_context(self, request: ContextSummaryRequest) -> ContextSummaryResult:
        """Write a new summary with one call of a tool-less agent on ``summary_model``.

        The summary agent has ``CONTEXT_SUMMARY_INSTRUCTIONS``, the agent's
        model settings, no tools, and plain text output.

        .. versionadded:: 0.3.0

        Args:
            request: The previous summary and the messages to summarize.

        Returns:
            The summary text and the usage of the call.

        Raises:
            ValueError: If no model is set, or the model returned no text.
            Exception: Any model or Pydantic AI error fails the attempt.
        """
        model = self.summary_model
        if model is None:
            raise ValueError("compaction needs a model: pass summary_model or model")
        summary_agent = Agent(
            model,
            instructions=CONTEXT_SUMMARY_INSTRUCTIONS,
            model_settings=self._agent.model_settings
            if isinstance(self._agent, Agent) and isinstance(self._agent.model_settings, dict)
            else None,
        )
        result = await summary_agent.run(build_context_summary_prompt(request))
        text = result.output.strip()
        if not text:
            raise ValueError("the summary model call returned no text")
        response = result.response
        return ContextSummaryResult(
            text=text,
            usage=_agent_usage(result.usage),
            model_call_metadata=ModelCallMetadata(
                request_model=response.model_name, response_id=response.provider_response_id
            ),
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        """Run one tool call through the agent, without a model call.

        A call that a tool source built goes to that source, found by the
        source name stored with the call, with the source's own reference and
        the tool Step's live context; the agent does not run.

        The runner starts an agent run whose history holds only the tool call
        and stops it once the tool returns. An approved call runs with
        ``RunContext.tool_call_approved`` set; a call that needed no approval
        runs without it. The run metadata carries the idempotency key, which
        ``deferred_tool_idempotency_key`` reads.

        Args:
            call: The approved record and its idempotency key.

        Returns:
            The tool's return value as the text the model sees, or the
            source's outcome. With a ``media_store``, image ``BinaryContent``
            that the tool returned is uploaded and returned as references.

            .. versionchanged:: 0.5.0
                Image ``BinaryContent`` becomes references with a ``media_store``.

        Raises:
            ToolExecutionError: The tool raised ``ModelRetry`` or ``ToolFailed``,
                ran out of its own retries, is unknown or external, or asked for
                approval while it ran; or the source raised it. The model sees
                the message.
            ToolOutcomeUnknownError: The source cannot tell whether the call
                took effect. The session records ``OUTCOME_UNKNOWN``.
            LookupError: A source built the call, but no source of that name
                is configured on this Worker, so the effect class decides.
            RuntimeError: The tool raised ``CallDeferred`` while it ran, so its
                work may have taken effect.
            UnexpectedModelBehavior: The tool body raised it, such as from a
                sub-agent run, so its work may have taken effect.
            Exception: Any other tool error fails the Step attempt. After the
                last attempt the session records ``FAILED`` for a ``READ`` call
                and ``OUTCOME_UNKNOWN`` for a write.
        """
        record = call.tool_call
        if self._tool_sources.is_source_tool_call(record):
            return await self._tool_sources.execute_deferred_tool_call(call)
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
        images = (
            []
            if self._media_store is None
            else await upload_tool_return_images(tool_return, self._media_store)
        )
        return ToolExecutionResult(output=tool_return.model_response_str(), images=images)

    def tool_call_step_options(self, tool_call: AgentToolCall) -> ToolCallStepOptions | None:
        """Return a tool source's options for one call's tool Step.

        A source that defines ``tool_call_step_options(tool_call)`` gives its
        calls their own timeout and retry policy, as ``ConnectorToolSource``
        does from each operation's manifest. The agent's own tools keep the
        tool Steps' defaults.

        .. versionadded:: 0.4.0

        Args:
            tool_call: The record whose tool Step starts.

        Returns:
            The source's options, or ``None`` for the defaults.

        Raises:
            LookupError: If a source built the call but no source of that name
                is configured on this Worker. The session then uses the
                defaults.
        """
        return self._tool_sources.tool_call_step_options(tool_call)

    async def _run_model_call(
        self,
        turn: AgentTurnContext,
        capability: DexModelStepCapability,
        source_tools: SourceToolListing,
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
            toolsets=_source_toolsets(source_tools) or None,
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
    tracing: AgentTracingOptions | None = None,
    deps_factory: Callable[[str], AgentDepsT] | None = None,
    model: Model | KnownModelName | str | None = None,
    compaction: CompactionPolicy | None = None,
    retained_message_limit: int | None = DEFAULT_RETAINED_MESSAGE_LIMIT,
    summary_model: Model | KnownModelName | str | None = None,
    tool_sources: Sequence[ToolSource] = (),
    tool_source_listing_timeout: timedelta = DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
    media_store: MediaStore | None = None,
    summary_attributes_indexed: bool = False,
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

    Tool sources add tools that always run in their own tool Step, reads
    included. ``ConnectorToolSource`` offers reviewed Connector operations
    this way: each call gets the operation's effect class, approval, Call ID,
    timeout, and retry policy from its manifest, as in an agent on the built-in loop.

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
        tracing: OpenTelemetry GenAI span options. ``None``, the default,
            uses ``AgentTracingOptions()``, which emits spans through the global
            tracer provider when the OpenTelemetry API is installed.
        deps_factory: Builds the agent's dependencies from the session ID on
            the Worker, before every model Step and tool Step. ``None`` passes
            ``None``.
        model: Model for every run instead of the agent's default model.
        compaction: When to summarize earlier turns so the model input fits the
            context window. ``None``, the default, never compacts. See
            ``AgentSessionOptions.compaction``.

            .. versionadded:: 0.3.0
        retained_message_limit: With ``compaction``, how many stored messages
            retention keeps. See ``AgentSessionOptions.retained_message_limit``.

            .. versionadded:: 0.3.0
        summary_model: Model of the summary call. ``None`` uses ``model``, else
            the agent's model.

            .. versionadded:: 0.3.0
        tool_sources: Tool sources, such as ``ConnectorToolSource``, offered as
            external tools of every model call. See ``PydanticAIRunner``. A
            ``tool_policies`` entry for a connector tool changes only its
            approval. Defaults to none.

            .. versionadded:: 0.4.0
        tool_source_listing_timeout: How long one ``RunModel`` waits for each
            source's ``list_tools``. Defaults to 10 seconds.

            .. versionadded:: 0.4.0
        media_store: The store that holds the session's images. With a store,
            user messages and tool results may carry images, which the model
            sees as ``BinaryContent``. ``None``, the default, makes the session
            refuse images. See ``PydanticAIRunner``.

            .. versionadded:: 0.5.0

        summary_attributes_indexed: Whether the session keeps the indexed
            session summary that ``list_agent_sessions`` reads. Defaults to
            ``False``. See ``AgentSessionOptions.summary_attributes_indexed``
            for what it stores and the index keys it registers.

            .. versionadded:: 0.6.0

    Returns:
        An ``AgentSessionFlow`` backed by a ``PydanticAIRunner``.

    Raises:
        ValueError: If ``flow_type`` is blank, an option is not positive, the
            agent has output validators without ``DeferredToolRequests`` in
            its ``output_type``, ``compaction`` is set and no model is, or a
            tool source is invalid as ``PydanticAIRunner`` describes.
        TypeError: If a tool source lacks a ``ToolSource`` method or a
            non-blank ``source_name``.
    """
    runner = PydanticAIRunner(
        agent,
        deps_factory=deps_factory,
        model=model,
        summary_model=summary_model,
        tool_sources=tool_sources,
        tool_source_listing_timeout=tool_source_listing_timeout,
        media_store=media_store,
    )
    if compaction is not None and runner.summary_model is None:
        raise ValueError("compaction needs a model: pass summary_model or model")
    return AgentSessionFlow(
        runner,
        flow_type=flow_type,
        options=AgentSessionOptions(
            tool_policies=tool_policies or {},
            default_tool_effect=default_tool_effect,
            event_stream_capacity_bytes=event_stream_capacity_bytes,
            tracing=AgentTracingOptions() if tracing is None else tracing,
            interrupt_deadline=interrupt_deadline,
            compaction=compaction,
            retained_message_limit=retained_message_limit,
            summary_attributes_indexed=summary_attributes_indexed,
        ),
    )


def _estimate_agent_prompt_tokens(agent: AbstractAgent[Any, Any]) -> int:
    wrapped_agent = agent
    while isinstance(wrapped_agent, WrapperAgent):
        wrapped_agent = wrapped_agent.wrapped
    # Pydantic AI exposes no public accessor for static prompts.
    parts = [str(prompt) for prompt in getattr(wrapped_agent, "_system_prompts", ())]
    for instruction in getattr(wrapped_agent, "_instructions", ()):
        text = getattr(instruction, "instruction", None)
        if isinstance(text, str):
            parts.append(text)
    for toolset in agent.toolsets:
        tools = getattr(toolset, "tools", None)
        # Only a FunctionToolset lists its tools; MCP servers list theirs at run time.
        for tool in tools.values() if isinstance(tools, Mapping) else ():
            definition = getattr(tool, "tool_def", None)
            if definition is not None:
                parts.extend(
                    (
                        definition.name,
                        definition.description or "",
                        json.dumps(definition.parameters_json_schema),
                    )
                )
    return sum(map(estimate_text_tokens, parts))


def _function_tool_names(agent: AbstractAgent[Any, Any]) -> set[str]:
    names: set[str] = set()
    for toolset in agent.toolsets:
        tools = getattr(toolset, "tools", None)
        # Only a FunctionToolset lists its tools before a run.
        if isinstance(tools, Mapping):
            names.update(str(name) for name in tools)
    return names


def _source_toolsets(source_tools: SourceToolListing) -> list[AbstractToolset[Any]]:
    toolsets: list[AbstractToolset[Any]] = []
    if source_tools.tools:
        # External tools never run, so the run ends and the call reaches its tool Step.
        toolsets.append(
            ExternalToolset(
                [
                    ToolDefinition(
                        name=tool.name,
                        description=tool.description,
                        parameters_json_schema=dict(tool.input_schema),
                    )
                    for tool in source_tools.tools
                ]
            )
        )
    if source_tools.replay_only_tools:
        # A recorded call replays its supplied result; a new call reads the refusal, never a Step.
        toolsets.append(
            FunctionToolset(
                [
                    Tool.from_schema(
                        _SourceToolRefusal(source_tools.not_offered_result(tool.name)),
                        name=tool.name,
                        description=tool.description,
                        json_schema=dict(tool.input_schema),
                    )
                    for tool in source_tools.replay_only_tools
                ]
            )
        )
    return toolsets


class _SourceToolRefusal:
    def __init__(self, refusal: str) -> None:
        self._refusal = refusal

    def __call__(self, **arguments: Any) -> str:
        return self._refusal


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
            output = (
                _parse_structured_output(outcome)
                if tool_call_id in structured_result_ids
                else outcome
            )
            # Placeholders after the output; the model request carries the bytes in the result.
            results.calls[tool_call_id] = ToolReturn(
                [output, *map(image_reference_placeholder, record.images)]
                if record.images
                else output
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
    turn: AgentTurnContext,
    requests: DeferredToolRequests,
    turn_messages: Sequence[ModelMessage],
    capability: DexModelStepCapability,
    source_tools: SourceToolListing,
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
        source_call = source_tools.deferred_tool_call(
            turn,
            tool_call_id=part.tool_call_id,
            tool_name=part.tool_name,
            arguments=part.args_as_json_str(),
        )
        if source_call is not None:
            tool_calls.append(source_call)
            continue
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
                and part.tool_name not in capability.replay_only_tool_names
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
