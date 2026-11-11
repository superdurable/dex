# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The agent framework runner for LangChain agents and LangGraph graphs."""

from __future__ import annotations

import asyncio
import inspect
import json
import threading
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from datetime import timedelta
from typing import Any, TypeAlias
from uuid import UUID, uuid4

from dex_agents import (
    CONTEXT_SUMMARY_INSTRUCTIONS,
    AgentFrameworkRunner,
    AgentImageReference,
    AgentToolCall,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    ContextSummaryRequest,
    ContextSummaryResult,
    DeferredToolCall,
    DeferredToolExecution,
    DeferredToolOutcome,
    FrameworkMessage,
    MediaStore,
    ModelCallMetadata,
    ToolCallsRequested,
    ToolCallStepOptions,
    ToolExecutionError,
    ToolExecutionResult,
    TurnCompleted,
    build_context_summary_note,
    build_context_summary_prompt,
    describe_tool_call_outcome,
    estimate_stored_input_tokens,
    estimate_text_tokens,
    select_turn_boundary_cutoff,
)
from dex_agents.stepwise import (
    DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
    FrameworkToolSourceRouter,
    SourceToolListing,
    ToolSource,
)
from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import (
    AIMessage,
    BaseMessage,
    HumanMessage,
    SystemMessage,
    ToolCall,
    ToolMessage,
)
from langchain_core.outputs import LLMResult
from langchain_core.runnables import RunnableConfig
from langchain_core.tools import BaseTool, ToolException
from langgraph.channels.delta import DeltaChannel
from langgraph.checkpoint.serde.base import SerializerProtocol
from langgraph.prebuilt import ToolNode
from langgraph.pregel import Pregel
from langgraph.types import Command, Interrupt, StateSnapshot
from pydantic import BaseModel, ValidationError
from pydantic_core import to_jsonable_python

from dex_agents_langgraph._checkpoints import (
    PendingInterrupt,
    TurnCheckpointSaver,
    decode_suspended_graph_run_state,
    encode_suspended_graph_run_state,
    restore_suspended_graph_checkpoints,
)
from dex_agents_langgraph._images import (
    image_reference_block,
    message_image_references,
    upload_content_images,
)
from dex_agents_langgraph.messages import (
    framework_messages_to_langchain_messages,
    langchain_message_to_framework_message,
)
from dex_agents_langgraph.tool_deferral import (
    TOOL_CALL_IDEMPOTENCY_KEY_CONFIGURABLE,
    _bind_run_model,
    _bind_tool_step,
    _deferred_tool_call_requests,
    _DeferredToolCallRequest,
    _recorded_outcomes_resume_value,
    _RecordedToolCallOutcome,
    _RunModelBinding,
    _source_tool_as_langchain_tool,
)

#: A compiled LangGraph graph, as ``create_agent`` and ``StateGraph.compile`` return it.
CompiledGraph: TypeAlias = Pregel[Any, Any, Any, Any]

#: A compiled graph, or a function that builds one on first use, optionally asynchronously.
CompiledGraphSource: TypeAlias = (
    CompiledGraph | Callable[[], CompiledGraph] | Callable[[], Awaitable[CompiledGraph]]
)


# LangChain model_provider values whose provider has a well-known gen_ai.provider.name value.
_GEN_AI_PROVIDER_NAMES = {
    "anthropic": "anthropic",
    "anthropic_bedrock": "aws.bedrock",
    "azure": "azure.ai.openai",
    "azure_openai": "azure.ai.openai",
    "bedrock": "aws.bedrock",
    "bedrock_converse": "aws.bedrock",
    "bedrock_mantle_anthropic": "aws.bedrock",
    "bedrock_mantle_openai": "aws.bedrock",
    "cohere": "cohere",
    "deepseek": "deepseek",
    "google_anthropic_vertex": "gcp.vertex_ai",
    # ChatGoogleGenerativeAI reports google_genai for both the Gemini API and Vertex AI.
    "google_genai": "gcp.gen_ai",
    "google_vertexai": "gcp.vertex_ai",
    "groq": "groq",
    "ibm": "ibm.watsonx.ai",
    "mistralai": "mistral",
    "openai": "openai",
    "perplexity": "perplexity",
    "xai": "x_ai",
}


class LangGraphAgentRunner(AgentFrameworkRunner[DeferredToolOutcome]):
    """Run a LangChain agent or a LangGraph graph inside an agent session Flow.

    Most applications build the Flow with ``langchain_agent_session_flow`` or
    ``langgraph_agent_session_flow``, which construct this runner. Construct
    it directly to pass it to ``AgentSessionFlow`` yourself.

    The graph must keep the conversation in a list of LangChain messages under
    ``messages_key``, with the ``add_messages`` reducer, as ``create_agent``
    and ``MessagesState`` do. Its tool calls must go through
    ``DurableToolCallMiddleware`` or a ``durable_tool_node``.

    The session runs in Step mode. Each ``RunModel`` Step calls
    ``run_model_step``, which runs the graph on a fresh, turn-local
    checkpointer that replaces any checkpointer compiled into the graph. The
    first model call of a turn replays every stored message as the graph
    input. The graph makes one model call, and the tool node pauses it with
    ``interrupt()`` for every tool call. The runner then returns
    ``ToolCallsRequested`` with the latest checkpoint and pending writes,
    serialized with the checkpointer's serializer, and one ``DeferredToolCall``
    per tool call in model order. The session runs each call in its own Step,
    in parallel, through ``execute_deferred_tool_call``. The next ``RunModel``
    restores the checkpoint and resumes the graph with ``Command(resume=...)``
    carrying every recorded result, and the graph makes its next model call.
    When the graph finishes, every message it added to the message state is
    returned in ``TurnCompleted``.

    During ``RunModel`` the runner streams visible model text and provider
    reasoning summaries to ``AgentEvents``, and counts usage from each model
    call's ``usage_metadata``. It blocks every tool that starts inside
    ``RunModel`` and then fails the call, so no tool runs outside its own Step.

    The runner keeps no session state between calls. It builds the graph from
    a factory once, on first use, and shares it across sessions.

    Tool sources, such as ``ConnectorToolSource``, add tools that the runner
    lists in every ``RunModel``. ``DurableToolCallMiddleware`` adds them to
    every model request of a ``create_agent`` agent; a custom graph binds
    ``get_current_tool_source_tools()`` in its model node. Their bodies never
    run: every call is deferred and runs once in its own tool Step through
    the source that built it. The source picks the effect class, the
    approval, the write ordering, and the tool Step's timeout and retry
    policy. The runner polls no tool tasks, so a source call that starts one
    records ``FAILED`` for a ``READ`` call and ``OUTCOME_UNKNOWN`` otherwise.

    The runner implements ``ContextCompactionRunner``. With a ``summary_model``
    and ``AgentSessionOptions.compaction``, the session compacts at turn
    boundaries: the summary enters as the first message of the graph input. A
    resumed model call restores a checkpoint that already holds its input, so
    it never compacts. Without a policy nothing changes.

    With a ``media_store``, the runner also implements
    ``ImageUserMessageBuilder``, so user messages and tool results may carry
    images. See the ``media_store`` argument of the constructor.

    Example:
        >>> runner = LangGraphAgentRunner(agent)
        >>> flow = AgentSessionFlow(
        ...     runner,
        ...     flow_type="SupportAgent",
        ...     options=AgentSessionOptions(
        ...         tool_policies={"search_orders": ToolPolicy(ToolEffect.READ)}
        ...     ),
        ... )
    """

    def __init__(
        self,
        graph: CompiledGraphSource,
        *,
        tools: Sequence[BaseTool] = (),
        messages_key: str = "messages",
        checkpoint_serializer: SerializerProtocol | None = None,
        summary_model: BaseChatModel | None = None,
        tool_sources: Sequence[ToolSource] = (),
        tool_source_listing_timeout: timedelta = DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
        media_store: MediaStore | None = None,
    ) -> None:
        """Create a runner for one compiled graph.

        A graph passed directly is checked now. A factory runs on the Worker's
        event loop at the first model or tool call, which keeps model clients
        and tool connections out of processes that only start sessions. Its
        graph is checked then, and a failure fails that Step.

        Args:
            graph: A compiled graph, or a function that returns one. The
                function may be ``async``, for example to load MCP tools.
            tools: Tools that tool call Steps may run, in addition to the tools
                of every ``ToolNode`` in the graph and its subgraphs. Pass tools
                that middleware adds dynamically. A tool here also settles a
                name that two tool nodes use for different tools.
            messages_key: State key of the message list. Defaults to
                ``"messages"``.
            checkpoint_serializer: Serializer for the turn-local checkpoints
                and the stored run state, such as an
                ``EncryptedSerializer``. ``None`` uses LangGraph's default
                ``JsonPlusSerializer``. Every Worker must use the same one.
            summary_model: Chat model of the ``CompactContext`` summary call.
                A compiled graph does not expose its model, so compaction
                needs this. ``None``, the default, makes every summary call
                fail.

                .. versionadded:: 0.3.0
            tool_sources: Tool sources, such as ``ConnectorToolSource``, whose
                tools every ``RunModel`` lists and offers to the model. Each
                source has a unique ``source_name``, which the runner stores
                with every call it builds, so the tool Step reaches that
                source on any Worker. A ``tool_policies`` entry for a
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
                accepts images. A user message is stored as a ``HumanMessage``
                whose content is a text block, then one placeholder block per
                image: ``{"type": "image", "url": <uri>, "mime_type": ...,
                "extras": {"dex_image_reference": <reference>}}``. A tool call
                record's images go into its ``ToolMessage`` content after the
                output text block, as the same placeholders. Stored messages and
                checkpoints never hold bytes. Right before each model call,
                ``DurableToolCallMiddleware``, or ``inline_image_references`` in
                the model node of a custom graph, reads the bytes with
                ``load_images`` and replaces each placeholder with
                ``{"type": "image", "base64": ..., "mime_type": ...}``. A failed
                read fails the model call, which retries. A tool of the graph
                that returns base64 image blocks has the images uploaded in its
                tool Step and recorded as references. ``None``, the default,
                keeps the runner without image support, so the session refuses
                images.

                .. versionadded:: 0.5.0

        Raises:
            TypeError: If ``graph`` is neither a compiled graph nor callable,
                or a tool source lacks a ``ToolSource`` method or a non-blank
                ``source_name``.
            ValueError: If a compiled ``graph`` has no ``messages_key`` channel,
                uses a ``DeltaChannel``, or has two tool nodes whose different
                tools share a name that ``tools`` does not settle; if two tool
                sources share a ``source_name``; if a source's
                ``declared_tool_names`` repeat a tool of ``tools``, a tool of a
                compiled ``graph``, or another source's tool; or if
                ``tool_source_listing_timeout`` is not positive.
        """
        if not isinstance(graph, Pregel) and not callable(graph):
            raise TypeError("graph must be a compiled LangGraph graph or a function returning one")
        self._graph_factory = None if isinstance(graph, Pregel) else graph
        self._explicit_tools = {tool.name: tool for tool in tools}
        self._messages_key = messages_key
        self._checkpoint_serializer = checkpoint_serializer
        self._summary_model = summary_model
        self._graph_load_lock = asyncio.Lock()
        self._loaded_graph = (
            _inspect_compiled_graph(graph, messages_key, self._explicit_tools)
            if isinstance(graph, Pregel)
            else None
        )
        self._tool_sources = FrameworkToolSourceRouter(
            tool_sources,
            reserved_tool_names=set(
                self._explicit_tools
                if self._loaded_graph is None
                else self._loaded_graph.tools_by_name
            ),
            listing_timeout=tool_source_listing_timeout,
        )
        self._media_store = media_store
        if media_store is not None:
            # The session accepts images only from a runner that has this method.
            self.build_user_message_with_images = self._build_user_message_with_images

    def build_user_message(self, text: str) -> FrameworkMessage:
        """Store user text as a LangChain ``HumanMessage``.

        The session also stores the core's turn-failure note through this
        method, so the note replays to the model as a user message.

        Args:
            text: The user message text.

        Returns:
            A ``MessageRole.USER`` message holding a ``HumanMessage`` with a
            new ``"dex-user-<hex>"`` ID.
        """
        return langchain_message_to_framework_message(
            HumanMessage(content=text, id=f"dex-user-{uuid4().hex}")
        )

    def _build_user_message_with_images(
        self, text: str, images: Sequence[AgentImageReference]
    ) -> FrameworkMessage:
        """Store user text and images as one LangChain ``HumanMessage`` with image placeholders.

        It is ``build_user_message_with_images`` of a runner built with a
        ``media_store``. The content is a text block, then one placeholder
        image block per image, in order, whose ``url`` is the reference's
        ``uri`` and whose ``extras`` hold the reference. Before each model
        call the placeholders are replaced with the bytes.

        .. versionadded:: 0.5.0

        Args:
            text: The user message text.
            images: The message's images, at least one.

        Returns:
            A ``MessageRole.USER`` message holding a ``HumanMessage`` with a
            new ``"dex-user-<hex>"`` ID.
        """
        content: list[str | dict[str, Any]] = [
            {"type": "text", "text": text},
            *map(image_reference_block, images),
        ]
        return langchain_message_to_framework_message(
            HumanMessage(content=content, id=f"dex-user-{uuid4().hex}")
        )

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        """Run or resume the graph for one model call of the turn.

        Args:
            turn: Committed session state for this model call.

        Returns:
            ``ToolCallsRequested`` when the model asked for tools;
            ``TurnCompleted`` with every message the turn added once the graph
            finishes.

        Raises:
            RuntimeError: If a tool started inside the graph, the graph did not
                resume a recorded tool call, raised an interrupt that is not a
                deferred tool call, or paused at a static ``interrupt_before`` or
                ``interrupt_after`` node.
            ValueError: If the stored history or run state cannot be read, or a
                tool source listed a name that a tool of the graph or another
                source's tool uses.
        """
        loaded_graph = await self._load_graph()
        source_tools = await self._tool_sources.list_source_tools(
            turn, reserved_tool_names=loaded_graph.tools_by_name.keys()
        )
        history = framework_messages_to_langchain_messages(turn.messages)
        if turn.context_summary is not None:
            # A fixed ID keeps the summary out of the turn's new messages, resumed calls too.
            summary_message_id = f"dex-context-summary-{turn.context_summary.generation}"
            note = build_context_summary_note(turn.context_summary)
            history.insert(0, HumanMessage(content=note, id=summary_message_id))
        saver = TurnCheckpointSaver(serde=self._checkpoint_serializer)
        graph = loaded_graph.graph.copy(update={"checkpointer": saver})
        turn_callbacks = _TurnCallbackHandler()
        resumed_tool_call_ids: set[str] = set()
        if turn.framework_run_state is None:
            thread_id = turn.flow_id
            graph_input: Any = {self._messages_key: history}
        else:
            run_state = decode_suspended_graph_run_state(turn.framework_run_state)
            thread_id = run_state.thread_id
            await restore_suspended_graph_checkpoints(saver, run_state)
            graph_input = Command(resume=_resume_values(turn, run_state.interrupts))
            resumed_tool_call_ids = {
                tool_call_id
                for interrupt in run_state.interrupts
                for tool_call_id in interrupt.tool_call_ids
            }
        config: RunnableConfig = {
            "configurable": {"thread_id": thread_id},
            "callbacks": [turn_callbacks],
        }
        binding = _RunModelBinding(
            turn=turn,
            tools_by_name=loaded_graph.tools_by_name,
            source_tools=[_source_tool_as_langchain_tool(tool) for tool in source_tools.tools],
            source_tool_listing=source_tools,
            media_store=self._media_store,
        )
        with _bind_run_model(binding):
            async for streamed in graph.astream(
                graph_input,
                config,
                stream_mode="messages",
                subgraphs=True,
                durability="exit",
            ):
                _write_streamed_message(turn, streamed)
        turn_callbacks.raise_if_tool_was_blocked()
        unresumed = sorted(resumed_tool_call_ids - binding.resumed_tool_call_ids)
        if unresumed:
            raise RuntimeError(
                f"the graph did not resume tool calls {unresumed} with their recorded results; "
                "a subgraph compiled with checkpointer=False restarts and cannot resume them"
            )
        snapshot = await graph.aget_state(saver.latest_checkpoint_config(thread_id))
        usage = turn_callbacks.recorded_usage()
        model_call_metadata = turn_callbacks.model_call_metadata()
        if snapshot.interrupts:
            return await self._request_tool_calls(
                turn, source_tools, saver, thread_id, snapshot, usage, model_call_metadata
            )
        if snapshot.next:
            raise RuntimeError(
                f"the graph paused before {list(snapshot.next)} without a deferred tool call; "
                "agent sessions do not support interrupt_before or interrupt_after"
            )
        return TurnCompleted(
            new_messages=self._new_turn_messages(snapshot, history),
            usage=usage,
            model_call_metadata=model_call_metadata,
        )

    @property
    def summary_model(self) -> BaseChatModel | None:
        """Return the chat model that writes a ``ContextSummary``.

        .. versionadded:: 0.3.0

        Returns:
            The constructor's ``summary_model``, or ``None``.
        """
        return self._summary_model

    def estimate_input_tokens(self, turn: AgentTurnContext) -> int:
        """Estimate the next model input from the stored messages and the known tools.

        The graph builds the request, and a compiled graph does not expose its
        system prompt or middleware. This counts every stored message as
        stored, the summary, the checkpoint of a resumed call, and the
        definitions of the tools the runner knows once the graph is loaded.
        Cover the system prompt with ``CompactionPolicy.reserved_input_tokens``.

        .. versionadded:: 0.3.0

        Args:
            turn: The context of the next model call.

        Returns:
            The estimated input tokens, from ``estimate_text_tokens``.
        """
        tools = (
            self._explicit_tools if self._loaded_graph is None else self._loaded_graph.tools_by_name
        )
        tool_tokens = sum(
            estimate_text_tokens(f"{tool.name} {tool.description} {json.dumps(tool.args)}")
            for tool in tools.values()
        )
        return estimate_stored_input_tokens(turn, fixed_input_tokens=tool_tokens)

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
        """Write a new summary with one call of ``summary_model``, outside the graph.

        .. versionadded:: 0.3.0

        Args:
            request: The previous summary and the messages to summarize.

        Returns:
            The summary text and the usage of the call.

        Raises:
            ValueError: If the runner has no ``summary_model``, or the model
                returned no text.
            Exception: Any error of the chat model fails the attempt.
        """
        if self._summary_model is None:
            raise ValueError("compaction needs LangGraphAgentRunner(summary_model=...)")
        response = await self._summary_model.ainvoke(
            [
                SystemMessage(content=CONTEXT_SUMMARY_INSTRUCTIONS),
                HumanMessage(content=build_context_summary_prompt(request)),
            ]
        )
        if not isinstance(response, AIMessage) or not response.text.strip():
            raise ValueError("the summary model call returned no text")
        metadata = response.response_metadata
        return ContextSummaryResult(
            text=response.text.strip(),
            usage=_model_call_usage(response),
            model_call_metadata=ModelCallMetadata(
                response_model=_metadata_text(metadata, "model_name"),
                provider_name=_gen_ai_provider_name(_metadata_text(metadata, "model_provider")),
                response_id=_metadata_text(metadata, "id"),
            ),
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        """Run one tool call once, in its own Step, with the tool's own ``ainvoke``.

        A call that a tool source built goes to that source instead, found by
        the source name stored with the call, with the source's own reference
        and the tool Step's live context.

        The arguments are first validated against the tool's
        ``tool_call_schema``. The tool then receives a ``ToolCall`` with the
        recorded ID and arguments, and its ``RunnableConfig`` carries
        ``call.idempotency_key`` unchanged, which
        ``get_tool_call_idempotency_key`` reads. A sub-agent that the tool
        invokes with ``DurableToolCallMiddleware`` runs its own tools inline,
        as part of this Step.

        Args:
            call: The approved record and its idempotency key.

        Returns:
            The tool's output text. Content that is not a string is JSON text.
            With a ``media_store``, base64 image blocks in the content are
            uploaded and returned as references, and the output is the rest of
            the content. For a source call, the source's outcome.

            .. versionchanged:: 0.5.0
                Base64 image blocks become references with a ``media_store``.

        Raises:
            ToolExecutionError: The graph has no such tool, the arguments do not
                match the tool's schema, the tool raised ``ToolException``, or
                it returned an error ``ToolMessage``; or the source raised it.
            ToolOutcomeUnknownError: The source cannot tell whether the call
                took effect. The session records ``OUTCOME_UNKNOWN``.
            LookupError: A source built the call, but no source of that name
                is configured on this Worker, so the effect class decides.
            TypeError: The tool returned something other than a
                ``ToolMessage``, such as a graph ``Command``.
            Exception: Any other exception from the tool, including a
                ``ValidationError`` raised inside its body, fails the Step
                attempt. Once the attempts run out, the session records
                ``FAILED`` for a ``READ`` call and ``OUTCOME_UNKNOWN`` for a
                write, because the write may have taken effect.
        """
        record = call.tool_call
        if self._tool_sources.is_source_tool_call(record):
            return await self._tool_sources.execute_deferred_tool_call(call)
        loaded_graph = await self._load_graph()
        tool = loaded_graph.tools_by_name.get(record.tool_name)
        if tool is None:
            raise ToolExecutionError(f"the agent has no tool named {record.tool_name}")
        arguments = _validated_tool_arguments(tool, record.arguments)
        tool_call = ToolCall(
            name=record.tool_name, args=arguments, id=record.tool_call_id, type="tool_call"
        )
        config: RunnableConfig = {
            "configurable": {TOOL_CALL_IDEMPOTENCY_KEY_CONFIGURABLE: call.idempotency_key}
        }
        with _bind_tool_step():
            try:
                result = await tool.ainvoke(tool_call, config)
            except ToolException as error:
                raise ToolExecutionError(str(error)) from error
        if not isinstance(result, ToolMessage):
            raise TypeError(
                f"tool {record.tool_name} returned {type(result).__name__}; "
                "a tool of an agent session must return content, not a graph Command"
            )
        images: list[AgentImageReference] = []
        if self._media_store is not None and isinstance(result.content, list):
            remaining, images = await upload_content_images(
                result.content, self._media_store, record.tool_name
            )
            result = result.model_copy(update={"content": remaining})
        output = _tool_message_output(result)
        if result.status == "error":
            raise ToolExecutionError(output)
        return ToolExecutionResult(output=output, images=images)

    def tool_call_step_options(self, tool_call: AgentToolCall) -> ToolCallStepOptions | None:
        """Return a tool source's options for one call's tool Step.

        A source that defines ``tool_call_step_options(tool_call)`` gives its
        calls their own timeout and retry policy, as ``ConnectorToolSource``
        does from each operation's manifest. The graph's own tools keep the
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

    async def _load_graph(self) -> _LoadedGraph:
        if self._loaded_graph is not None:
            return self._loaded_graph
        async with self._graph_load_lock:
            if self._loaded_graph is None and self._graph_factory is not None:
                built = self._graph_factory()
                if inspect.isawaitable(built):
                    built = await built
                self._loaded_graph = _inspect_compiled_graph(
                    built, self._messages_key, self._explicit_tools
                )
        if self._loaded_graph is None:
            raise RuntimeError("the runner has neither a compiled graph nor a graph factory")
        return self._loaded_graph

    async def _request_tool_calls(
        self,
        turn: AgentTurnContext,
        source_tools: SourceToolListing,
        saver: TurnCheckpointSaver,
        thread_id: str,
        snapshot: StateSnapshot,
        usage: AgentUsage,
        model_call_metadata: ModelCallMetadata | None,
    ) -> ToolCallsRequested:
        requests: list[_DeferredToolCallRequest] = []
        pending_interrupts: list[PendingInterrupt] = []
        for graph_interrupt in snapshot.interrupts:
            interrupt_requests = _deferred_tool_call_requests(graph_interrupt.value)
            if interrupt_requests is None:
                raise RuntimeError(_foreign_interrupt_message(graph_interrupt))
            requests.extend(interrupt_requests)
            pending_interrupts.append(
                PendingInterrupt(
                    interrupt_id=graph_interrupt.id,
                    tool_call_ids=[request.tool_call_id for request in interrupt_requests],
                )
            )
        model_order = _tool_call_model_order(snapshot, self._messages_key)
        ordered = sorted(
            enumerate(requests),
            key=lambda indexed: (
                model_order.get(indexed[1].tool_call_id, len(model_order)),
                indexed[0],
            ),
        )
        return ToolCallsRequested(
            framework_run_state=await encode_suspended_graph_run_state(
                saver, thread_id, pending_interrupts
            ),
            tool_calls=[
                # The source builds its calls, so its effect class and write ordering apply.
                source_tools.deferred_tool_call(
                    turn,
                    tool_call_id=request.tool_call_id,
                    tool_name=request.tool_name,
                    arguments=request.arguments,
                )
                or DeferredToolCall(
                    tool_call_id=request.tool_call_id,
                    tool_name=request.tool_name,
                    arguments=request.arguments,
                    policy=request.policy,
                )
                for _, request in ordered
            ],
            usage=usage,
            model_call_metadata=model_call_metadata,
        )

    def _new_turn_messages(
        self,
        snapshot: StateSnapshot,
        history: Sequence[BaseMessage],
    ) -> list[FrameworkMessage]:
        final_messages = _state_messages(snapshot, self._messages_key)
        if final_messages is None:
            raise RuntimeError(f"the graph state has no message list under {self._messages_key!r}")
        stored_message_ids = {message.id for message in history}
        return [
            langchain_message_to_framework_message(message)
            for message in final_messages
            if message.id not in stored_message_ids
        ]


@dataclass(frozen=True)
class _LoadedGraph:
    graph: CompiledGraph
    tools_by_name: Mapping[str, BaseTool]


@dataclass(frozen=True)
class _RecordedModelCall:
    request_model: str | None
    response_model: str | None
    provider_name: str | None
    response_id: str | None
    finish_reason: str | None


class _TurnCallbackHandler(BaseCallbackHandler):
    # Synchronous inline handlers run before the tool body, and raise_error lets them stop it.
    run_inline = True
    raise_error = True

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._usage = AgentUsage()
        self._blocked_tool_names: list[str] = []
        self._request_models_by_run_id: dict[UUID, str | None] = {}
        self._model_calls: list[_RecordedModelCall] = []

    def on_chat_model_start(
        self,
        serialized: dict[str, Any],
        messages: list[list[BaseMessage]],
        *,
        run_id: UUID,
        metadata: dict[str, Any] | None = None,
        **kwargs: Any,
    ) -> None:
        # LangChain puts the chat model's _get_ls_params, such as ls_model_name, in run metadata.
        request_model = _metadata_text(metadata or {}, "ls_model_name")
        if any(message_image_references(message) for batch in messages for message in batch):
            raise RuntimeError(
                "an image reference reached the chat model; call inline_image_references in "
                "the model node of a custom graph"
            )
        with self._lock:
            self._request_models_by_run_id[run_id] = request_model

    def on_llm_end(self, response: LLMResult, *, run_id: UUID, **kwargs: Any) -> None:
        recorded = AgentUsage(model_calls=1)
        generations = response.generations[0] if response.generations else []
        generation = generations[0] if generations else None
        message = getattr(generation, "message", None)
        response_metadata: Mapping[str, Any] = {}
        if isinstance(message, AIMessage):
            recorded = _model_call_usage(message)
            response_metadata = message.response_metadata
        with self._lock:
            self._usage = self._usage.combined_with(recorded)
            self._model_calls.append(
                _RecordedModelCall(
                    request_model=self._request_models_by_run_id.pop(run_id, None),
                    response_model=_metadata_text(response_metadata, "model_name"),
                    provider_name=_gen_ai_provider_name(
                        _metadata_text(response_metadata, "model_provider")
                    ),
                    response_id=_metadata_text(response_metadata, "id"),
                    finish_reason=_metadata_text(response_metadata, "finish_reason")
                    or _metadata_text(response_metadata, "stop_reason"),
                )
            )

    def on_tool_start(
        self,
        serialized: dict[str, Any],
        input_str: str,
        *,
        run_id: UUID,
        **kwargs: Any,
    ) -> None:
        tool_name = str(serialized.get("name", ""))
        with self._lock:
            self._blocked_tool_names.append(tool_name)
        raise RuntimeError(_blocked_tool_message([tool_name]))

    def raise_if_tool_was_blocked(self) -> None:
        with self._lock:
            blocked = list(self._blocked_tool_names)
        if blocked:
            raise RuntimeError(_blocked_tool_message(blocked))

    def recorded_usage(self) -> AgentUsage:
        with self._lock:
            return self._usage

    def model_call_metadata(self) -> ModelCallMetadata | None:
        with self._lock:
            calls = list(self._model_calls)
        if not calls:
            return None
        # Response-specific values describe one response; several calls keep only shared values.
        single_call = calls[0] if len(calls) == 1 else None
        return ModelCallMetadata(
            request_model=_value_shared_by_every_call([call.request_model for call in calls]),
            response_model=_value_shared_by_every_call([call.response_model for call in calls]),
            provider_name=_value_shared_by_every_call([call.provider_name for call in calls]),
            response_id=None if single_call is None else single_call.response_id,
            finish_reasons=()
            if single_call is None or single_call.finish_reason is None
            else (single_call.finish_reason,),
        )


def _inspect_compiled_graph(
    graph: object,
    messages_key: str,
    explicit_tools: Mapping[str, BaseTool],
) -> _LoadedGraph:
    if not isinstance(graph, Pregel):
        raise TypeError(f"the graph factory returned {type(graph).__name__}, not a compiled graph")
    if messages_key not in graph.channels:
        raise ValueError(f"the graph state has no {messages_key!r} channel for the conversation")
    tools_by_name: dict[str, BaseTool] = {}
    ambiguous_tool_names: set[str] = set()
    for compiled in _graph_and_subgraphs(graph):
        for channel_name, channel in compiled.channels.items():
            if isinstance(channel, DeltaChannel):
                raise ValueError(
                    f"state channel {channel_name!r} is a DeltaChannel, which agent sessions "
                    "cannot store while tool calls run"
                )
        for node in compiled.nodes.values():
            if not isinstance(node.bound, ToolNode):
                continue
            for tool_name, tool in node.bound.tools_by_name.items():
                known = tools_by_name.setdefault(tool_name, tool)
                if known is not tool:
                    ambiguous_tool_names.add(tool_name)
    ambiguous_tool_names -= explicit_tools.keys()
    if ambiguous_tool_names:
        raise ValueError(
            f"tool nodes of the graph use different tools named {sorted(ambiguous_tool_names)}; "
            "rename them, or pass the tool that tool call Steps should run in tools="
        )
    tools_by_name.update(explicit_tools)
    return _LoadedGraph(graph=graph, tools_by_name=tools_by_name)


def _graph_and_subgraphs(graph: CompiledGraph) -> list[CompiledGraph]:
    # get_subgraphs skips subgraphs compiled with checkpointer=False, so nodes are walked too.
    found: list[CompiledGraph] = [graph]
    for compiled in found:
        candidates = [node.bound for node in compiled.nodes.values()]
        candidates.extend(subgraph for _, subgraph in compiled.get_subgraphs())
        for candidate in candidates:
            if isinstance(candidate, Pregel) and all(candidate is not known for known in found):
                found.append(candidate)
    return found


def _resume_values(
    turn: AgentTurnContext,
    interrupts: Sequence[PendingInterrupt],
) -> dict[str, dict[str, Any]]:
    resume_values: dict[str, dict[str, Any]] = {}
    for pending in interrupts:
        outcomes: list[_RecordedToolCallOutcome] = []
        for tool_call_id in pending.tool_call_ids:
            record = turn.recorded_tool_call(tool_call_id)
            if record is None or not record.status.is_final:
                raise RuntimeError(f"tool call {tool_call_id} has no final record")
            outcomes.append(
                _RecordedToolCallOutcome(
                    tool_call_id=tool_call_id,
                    tool_name=record.tool_name,
                    status=record.status,
                    output=describe_tool_call_outcome(record),
                    images=record.images,
                )
            )
        resume_values[pending.interrupt_id] = _recorded_outcomes_resume_value(outcomes)
    return resume_values


def _validated_tool_arguments(tool: BaseTool, arguments_json: str) -> dict[str, Any]:
    try:
        arguments = json.loads(arguments_json)
    except json.JSONDecodeError as error:
        raise ToolExecutionError(f"the tool arguments are not JSON: {error}") from error
    if not isinstance(arguments, dict):
        raise ToolExecutionError("the tool arguments are not a JSON object")
    schema = tool.tool_call_schema
    if isinstance(schema, type) and issubclass(schema, BaseModel):
        try:
            schema.model_validate(arguments)
        except ValidationError as error:
            raise ToolExecutionError(f"invalid tool arguments: {error}") from error
    return arguments


def _state_messages(snapshot: StateSnapshot, messages_key: str) -> list[BaseMessage] | None:
    values = snapshot.values if isinstance(snapshot.values, dict) else {}
    messages = values.get(messages_key)
    if not isinstance(messages, list):
        return None
    return [message for message in messages if isinstance(message, BaseMessage)]


def _tool_call_model_order(snapshot: StateSnapshot, messages_key: str) -> dict[str, int]:
    order: dict[str, int] = {}
    for message in _state_messages(snapshot, messages_key) or []:
        if isinstance(message, AIMessage):
            for tool_call in message.tool_calls:
                order.setdefault(tool_call["id"] or "", len(order))
    return order


def _write_streamed_message(turn: AgentTurnContext, streamed: Any) -> None:
    # With subgraphs=True, messages mode yields (namespace, (message, metadata)).
    _, (message, _) = streamed
    if not isinstance(message, AIMessage):
        return
    for block in message.content_blocks:
        if block["type"] == "reasoning":
            turn.events.write_reasoning_delta(str(block.get("reasoning", "")))
    turn.events.write_text_delta(message.text)


def _model_call_usage(message: AIMessage) -> AgentUsage:
    usage_metadata = message.usage_metadata
    if usage_metadata is None:
        return AgentUsage(model_calls=1)
    input_details = usage_metadata.get("input_token_details") or {}
    output_details = usage_metadata.get("output_token_details") or {}
    return AgentUsage(
        model_calls=1,
        input_tokens=usage_metadata.get("input_tokens", 0),
        output_tokens=usage_metadata.get("output_tokens", 0),
        cached_input_tokens=input_details.get("cache_read", 0),
        reasoning_tokens=output_details.get("reasoning", 0),
    )


def _gen_ai_provider_name(model_provider: str | None) -> str | None:
    # Unknown providers pass through unchanged.
    if model_provider is None:
        return None
    return _GEN_AI_PROVIDER_NAMES.get(model_provider, model_provider)


def _metadata_text(metadata: Mapping[str, Any], key: str) -> str | None:
    value = metadata.get(key)
    return value if isinstance(value, str) and value else None


def _value_shared_by_every_call(values: Sequence[str | None]) -> str | None:
    return values[0] if len(set(values)) == 1 else None


def _foreign_interrupt_message(graph_interrupt: Interrupt) -> str:
    return (
        f"the graph raised interrupt {graph_interrupt.id} with value {graph_interrupt.value!r}; "
        "agent sessions resume only deferred tool calls"
    )


def _blocked_tool_message(tool_names: Sequence[str]) -> str:
    return (
        f"tool {', '.join(sorted(set(tool_names)))} started inside RunModel; every tool call "
        "must run in its own Step, so add DurableToolCallMiddleware to the agent, or build its "
        "ToolNode with durable_tool_node"
    )


def _tool_message_output(message: ToolMessage) -> str:
    if isinstance(message.content, str):
        return message.content
    return json.dumps(message.content, ensure_ascii=False, default=to_jsonable_python)
