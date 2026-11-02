# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""OpenTelemetry GenAI spans of the agent session Flow.

Nothing here imports OpenTelemetry at module import. The first Step that emits
a span imports the API once per process; without it, every method is a no-op.
Tracing never fails a Step: every OpenTelemetry call is guarded, logged, and
skipped on error, so a broken processor or exporter cannot rerun a tool.
"""

from __future__ import annotations

import functools
import importlib.metadata
import json
import logging
import re
import time
from collections.abc import Callable, Iterator, Sequence
from contextlib import contextmanager
from contextvars import Token
from types import ModuleType
from typing import TYPE_CHECKING, Any, TypeVar

from dex import Context

from dex_agents.persistence import AGENT_TRACE_CONTEXT, message_sequence_key
from dex_agents.runner import (
    AgentTurnContext,
    AgentTurnOutcome,
    FrameworkMessage,
    TurnCompleted,
    describe_tool_call_outcome,
)
from dex_agents.tracing import AgentTracingOptions, GenAISemanticConventionsVersion
from dex_agents.values import AgentMessage, AgentToolCall, AgentUsage, MessageRole, ToolCallStatus

if TYPE_CHECKING:
    from opentelemetry.context import Context as OpenTelemetryContext
    from opentelemetry.trace import Span, SpanContext, Tracer
    from opentelemetry.util.types import AttributeValue

TRACER_NAME = "dex_agents"
TOOL_EXECUTION_ERROR_TYPE = "ToolExecutionError"
_TRACEPARENT_PATTERN = re.compile(r"00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})")
_logger = logging.getLogger("dex_agents.tracing")
_has_logged_tracing_failure = False
_ResultT = TypeVar("_ResultT")


class ActivationSpan:
    """Expose the root span of an activation to the Step that starts it.

    Attributes:
        traceparent: The root's W3C ``traceparent`` when its span context is
            valid, sampled or not, else ``None``.
    """

    def __init__(self, traceparent: str | None) -> None:
        self.traceparent = traceparent


class ModelCallSpan:
    """Record the outcome of one ``RunModel`` attempt; this base records nothing."""

    def record_outcome(
        self, outcome: AgentTurnOutcome, appended_message_keys: Sequence[str]
    ) -> None:
        """Keep the outcome for the ``chat`` span, which is emitted when the attempt ends.

        Args:
            outcome: The runner's outcome.
            appended_message_keys: ``AgentMessages`` keys of ``TurnCompleted.new_messages``.
        """


class ToolCallSpan:
    """Record the final record of one tool Step attempt; this base records nothing."""

    def record_finished(self, finished: AgentToolCall) -> None:
        """Set the final status, and the result when content is captured.

        Args:
            finished: The final tool call record.
        """


_UNRECORDED_ACTIVATION = ActivationSpan(None)
_DISABLED_MODEL_CALL_SPAN = ModelCallSpan()
_DISABLED_TOOL_CALL_SPAN = ToolCallSpan()


class AgentSessionTracer:
    """Emit the spans of one agent session Flow type, or nothing when tracing is off.

    An activation's root ``invoke_agent`` span stores its ``traceparent`` in
    ``AgentTraceContext``. ``RunModel`` reads it to parent its ``chat`` span and
    copies it onto the tool call records it creates; an approval replaces a
    record's copy with its own root. Tool Steps parent their spans from the record.
    """

    def __init__(self, options: AgentTracingOptions, flow_type: str) -> None:
        self._options = options
        self._agent_name = options.agent_name or flow_type
        self._is_latest_version = (
            options.semantic_conventions_version is GenAISemanticConventionsVersion.V1_42_0_DEV
        )
        self._should_capture_content = (
            options.should_capture_message_content and self._is_latest_version
        )
        self._tracer: Tracer | None = None
        self._is_tracer_resolved = False

    @contextmanager
    def trace_user_message_activation(
        self,
        context: Context,
        step_type: str,
        turn_id: str,
        user_message_key: str,
        user_message_text: str,
    ) -> Iterator[ActivationSpan]:
        """Trace a Step that starts a turn with a user message as an activation root."""
        with self._trace_activation(
            context,
            step_type,
            turn_id,
            {
                "dex.agent.activation.trigger": "user_message",
                "dex.agent.user_message.key": user_message_key,
            },
            user_message_text,
        ) as activation:
            yield activation

    @contextmanager
    def trace_interrupt_activation(
        self,
        context: Context,
        step_type: str,
        turn_id: str,
        interrupt_id: str,
        is_approved: bool,
        is_deadline_passed: bool,
    ) -> Iterator[ActivationSpan]:
        """Trace a Step that applies an interrupt answer or deadline as an activation root."""
        trigger = "interrupt_deadline" if is_deadline_passed else "interrupt_resolution"
        with self._trace_activation(
            context,
            step_type,
            turn_id,
            {
                "dex.agent.activation.trigger": trigger,
                "dex.agent.interrupt.id": interrupt_id,
                "dex.agent.interrupt.approved": is_approved,
            },
            None,
        ) as activation:
            yield activation

    def read_activation_traceparent(self, context: Context) -> str | None:
        """Return the stored root ``traceparent`` that new tool call records carry."""
        return _call_safely("read the activation trace context", lambda: _read_traceparent(context))

    @contextmanager
    def trace_model_call(
        self,
        context: Context,
        step_type: str,
        turn: AgentTurnContext,
    ) -> Iterator[ModelCallSpan]:
        """Trace one ``RunModel`` attempt as a ``chat`` span, emitted when the attempt ends.

        The span starts after the runner returns, because its kind depends on
        the reported ``model_calls``. Meanwhile the activation root is current,
        so instrumentation of the model client nests under the activation.
        """
        tracer = self._resolve_tracer()
        if tracer is None:
            yield _DISABLED_MODEL_CALL_SPAN
            return
        start_time = time.time_ns()
        parent = _call_safely(
            "read the activation trace context", lambda: _parent_context(_read_traceparent(context))
        )
        token = (
            None if parent is None else _call_safely("activate the trace", lambda: _attach(parent))
        )
        model_call_span = _RecordingModelCallSpan(
            self._is_latest_version, self._should_capture_content
        )
        error: BaseException | None = None
        try:
            yield model_call_span
        except BaseException as raised:
            error = raised
            raise
        finally:
            if token is not None:
                _call_safely("restore the trace context", lambda: _detach(token))
            if parent is not None:
                _call_safely(
                    "emit the chat span",
                    lambda: model_call_span.emit(
                        tracer, parent, start_time, context, step_type, turn, error
                    ),
                )

    @contextmanager
    def trace_tool_call(
        self,
        context: Context,
        step_type: str,
        tool_call: AgentToolCall,
    ) -> Iterator[ToolCallSpan]:
        """Trace one tool Step attempt as an ``execute_tool`` span of the record's activation."""
        tracer = self._resolve_tracer()
        span = None
        if tracer is not None:
            span = _call_safely(
                "start the execute_tool span",
                lambda: _start_tool_call_span(tracer, self._agent_name, tool_call),
            )
        if span is None:
            yield _DISABLED_TOOL_CALL_SPAN
            return
        _call_safely(
            "set the execute_tool span attributes",
            lambda: self._set_tool_call_attributes(span, context, step_type, tool_call),
        )
        with _span_scope(span):
            yield _RecordingToolCallSpan(span, self._should_capture_content)

    @contextmanager
    def _trace_activation(
        self,
        context: Context,
        step_type: str,
        turn_id: str,
        activation_attributes: dict[str, AttributeValue],
        user_message_text: str | None,
    ) -> Iterator[ActivationSpan]:
        stored_traceparent = _call_safely(
            "read the activation trace context", lambda: _read_traceparent(context)
        )
        tracer = self._resolve_tracer()
        span = None
        if tracer is not None:
            span = _call_safely(
                "start the invoke_agent span",
                lambda: _start_activation_span(tracer, self._agent_name, stored_traceparent),
            )
        if span is None:
            activation = _UNRECORDED_ACTIVATION
            yield activation
        else:
            _call_safely(
                "set the invoke_agent span attributes",
                lambda: self._set_activation_attributes(
                    span, context, step_type, turn_id, activation_attributes, user_message_text
                ),
            )
            # An unsampled root stores its context too, so children follow its sampling decision.
            activation = ActivationSpan(
                _call_safely(
                    "read the invoke_agent span context",
                    lambda: _format_traceparent(span.get_span_context()),
                )
            )
            with _span_scope(span):
                yield activation
        # A root without a valid context must not leave the previous activation's context behind.
        _call_safely(
            "store the activation trace context",
            lambda: _store_traceparent(context, stored_traceparent, activation.traceparent),
        )

    def _set_activation_attributes(
        self,
        span: Span,
        context: Context,
        step_type: str,
        turn_id: str,
        activation_attributes: dict[str, AttributeValue],
        user_message_text: str | None,
    ) -> None:
        if not span.is_recording():
            return
        span.set_attributes(_step_attributes(context, step_type, turn_id))
        span.set_attributes(activation_attributes)
        if self._should_capture_content and user_message_text is not None:
            input_messages = [{"role": "user", "parts": [_text_part(user_message_text)]}]
            span.set_attribute("gen_ai.input.messages", _to_json(input_messages))

    def _set_tool_call_attributes(
        self,
        span: Span,
        context: Context,
        step_type: str,
        tool_call: AgentToolCall,
    ) -> None:
        if not span.is_recording():
            return
        span.set_attributes(_step_attributes(context, step_type, tool_call.turn_id))
        span.set_attribute("gen_ai.tool.call.id", tool_call.tool_call_id)
        span.set_attribute("dex.agent.tool.effect", str(tool_call.effect))
        if self._should_capture_content:
            span.set_attribute("gen_ai.tool.call.arguments", tool_call.arguments)

    def _resolve_tracer(self) -> Tracer | None:
        if not self._is_tracer_resolved:
            self._is_tracer_resolved = True
            self._tracer = _call_safely("create the tracer", self._create_tracer)
        return self._tracer

    def _create_tracer(self) -> Tracer | None:
        if not self._options.is_enabled:
            return None
        trace_api = _import_opentelemetry_trace()
        if trace_api is None:
            return None
        tracer: Tracer = trace_api.get_tracer(
            TRACER_NAME,
            _package_version(),
            self._options.tracer_provider,
            schema_url=self._options.semantic_conventions_version.schema_url,
        )
        return tracer


class _RecordingModelCallSpan(ModelCallSpan):
    def __init__(self, is_latest_version: bool, should_capture_content: bool) -> None:
        self._is_latest_version = is_latest_version
        self._should_capture_content = should_capture_content
        self._outcome: AgentTurnOutcome | None = None
        self._appended_message_keys: Sequence[str] = ()

    def record_outcome(
        self, outcome: AgentTurnOutcome, appended_message_keys: Sequence[str]
    ) -> None:
        self._outcome = outcome
        self._appended_message_keys = appended_message_keys

    def emit(
        self,
        tracer: Tracer,
        parent: OpenTelemetryContext,
        start_time: int,
        context: Context,
        step_type: str,
        turn: AgentTurnContext,
        error: BaseException | None,
    ) -> None:
        from opentelemetry.trace import SpanKind

        outcome = self._outcome
        metadata = None if outcome is None else outcome.model_call_metadata
        request_model = None if metadata is None else metadata.request_model
        attributes: dict[str, AttributeValue] = {"gen_ai.operation.name": "chat"}
        if request_model:
            attributes["gen_ai.request.model"] = request_model
        # One span for several model calls must not double count usage that provider spans report.
        is_single_model_call = outcome is None or outcome.usage.model_calls == 1
        span = tracer.start_span(
            f"chat {request_model}" if request_model else "chat",
            context=parent,
            kind=SpanKind.CLIENT if is_single_model_call else SpanKind.INTERNAL,
            attributes=attributes,
            start_time=start_time,
        )
        try:
            if span.is_recording():
                span.set_attributes(_step_attributes(context, step_type, turn.turn_id))
                if turn.messages:
                    span.set_attribute(
                        "dex.agent.input_messages.last_key",
                        message_sequence_key(turn.messages[-1].sequence),
                    )
                if self._should_capture_content:
                    span.set_attribute("gen_ai.input.messages", _to_json(_input_messages(turn)))
                if outcome is not None:
                    self._set_outcome_attributes(span, outcome, is_single_model_call)
                if error is not None:
                    _record_span_error(span, error)
        finally:
            span.end()

    def _set_outcome_attributes(
        self,
        span: Span,
        outcome: AgentTurnOutcome,
        is_single_model_call: bool,
    ) -> None:
        metadata = outcome.model_call_metadata
        if metadata is not None:
            if metadata.response_model:
                span.set_attribute("gen_ai.response.model", metadata.response_model)
            if metadata.provider_name:
                provider_attribute = (
                    "gen_ai.provider.name" if self._is_latest_version else "gen_ai.system"
                )
                span.set_attribute(provider_attribute, metadata.provider_name)
            if metadata.response_id:
                span.set_attribute("gen_ai.response.id", metadata.response_id)
            finish_reasons = [reason for reason in metadata.finish_reasons or () if reason]
            if finish_reasons:
                span.set_attribute("gen_ai.response.finish_reasons", finish_reasons)
        if is_single_model_call:
            self._set_usage_attributes(span, outcome.usage)
        if isinstance(outcome, TurnCompleted):
            produced_index = _first_produced_message_index(outcome.new_messages)
            span.set_attribute(
                "dex.agent.output_messages.keys", list(self._appended_message_keys[produced_index:])
            )
            output_messages = [
                _chat_message(message) for message in outcome.new_messages[produced_index:]
            ]
        else:
            span.set_attribute(
                "dex.agent.requested_tool_call.ids",
                [tool_call.tool_call_id for tool_call in outcome.tool_calls],
            )
            output_messages = [
                {
                    "role": "assistant",
                    "parts": [
                        _tool_call_part(
                            tool_call.tool_call_id, tool_call.tool_name, tool_call.arguments
                        )
                        for tool_call in outcome.tool_calls
                    ],
                }
            ]
        if self._should_capture_content:
            output_messages = [message for message in output_messages if message["parts"]]
            span.set_attribute("gen_ai.output.messages", _to_json(output_messages))

    def _set_usage_attributes(self, span: Span, usage: AgentUsage) -> None:
        # Runners report zero for counts the framework does not expose.
        if usage.input_tokens == 0 and usage.output_tokens == 0:
            return
        span.set_attribute("gen_ai.usage.input_tokens", usage.input_tokens)
        span.set_attribute("gen_ai.usage.output_tokens", usage.output_tokens)
        if not self._is_latest_version:
            return
        if usage.cached_input_tokens:
            span.set_attribute("gen_ai.usage.cache_read.input_tokens", usage.cached_input_tokens)
        if usage.reasoning_tokens:
            span.set_attribute("gen_ai.usage.reasoning.output_tokens", usage.reasoning_tokens)


class _RecordingToolCallSpan(ToolCallSpan):
    def __init__(self, span: Span, should_capture_content: bool) -> None:
        self._span = span
        self._should_capture_content = should_capture_content

    def record_finished(self, finished: AgentToolCall) -> None:
        _call_safely(
            "record the tool call outcome", lambda: self._set_finished_attributes(finished)
        )

    def _set_finished_attributes(self, finished: AgentToolCall) -> None:
        span = self._span
        if not span.is_recording():
            return
        from opentelemetry.trace import Status, StatusCode

        span.set_attribute("dex.agent.tool_call.status", str(finished.status))
        if finished.status is ToolCallStatus.FAILED:
            span.set_attribute("error.type", TOOL_EXECUTION_ERROR_TYPE)
            span.set_status(Status(StatusCode.ERROR, finished.error or TOOL_EXECUTION_ERROR_TYPE))
        elif self._should_capture_content and finished.output is not None:
            span.set_attribute("gen_ai.tool.call.result", finished.output)


def _call_safely(description: str, operation: Callable[[], _ResultT]) -> _ResultT | None:
    try:
        return operation()
    except Exception:
        _log_tracing_failure(description)
        return None


def _log_tracing_failure(description: str) -> None:
    global _has_logged_tracing_failure
    # The first failure logs a warning with its traceback; later ones log at debug level.
    level = logging.DEBUG if _has_logged_tracing_failure else logging.WARNING
    _has_logged_tracing_failure = True
    _logger.log(level, "tracing could not %s; the Step continues", description, exc_info=True)


@functools.cache
def _import_opentelemetry_trace() -> ModuleType | None:
    try:
        from opentelemetry import trace
    except ImportError:
        return None
    return trace


@functools.cache
def _package_version() -> str | None:
    try:
        return importlib.metadata.version("dex-agents")
    except importlib.metadata.PackageNotFoundError:
        return None


def _start_activation_span(
    tracer: Tracer,
    agent_name: str,
    previous_root_traceparent: str | None,
) -> Span:
    from opentelemetry.context import Context as OpenTelemetryContext
    from opentelemetry.trace import Link, SpanKind

    previous_root = _parse_traceparent(previous_root_traceparent)
    # An empty parent context keeps every activation a trace of its own.
    return tracer.start_span(
        f"invoke_agent {agent_name}",
        context=OpenTelemetryContext(),
        kind=SpanKind.INTERNAL,
        attributes={"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": agent_name},
        links=() if previous_root is None else (Link(previous_root),),
    )


def _start_tool_call_span(tracer: Tracer, agent_name: str, tool_call: AgentToolCall) -> Span:
    from opentelemetry.trace import SpanKind

    return tracer.start_span(
        f"execute_tool {tool_call.tool_name}",
        context=_parent_context(tool_call.trace_context),
        kind=SpanKind.INTERNAL,
        attributes={
            "gen_ai.operation.name": "execute_tool",
            "gen_ai.tool.name": tool_call.tool_name,
            "gen_ai.agent.name": agent_name,
        },
    )


@contextmanager
def _span_scope(span: Span) -> Iterator[None]:
    from opentelemetry import trace

    token = _call_safely("activate the span", lambda: _attach(trace.set_span_in_context(span)))
    try:
        yield
    except BaseException as error:
        _call_safely(
            "record the error on the span", functools.partial(_record_span_error, span, error)
        )
        raise
    finally:
        if token is not None:
            _call_safely("restore the trace context", lambda: _detach(token))
        _call_safely("end the span", span.end)


def _attach(otel_context: OpenTelemetryContext) -> Token[OpenTelemetryContext]:
    from opentelemetry import context as context_api

    return context_api.attach(otel_context)


def _detach(token: Token[OpenTelemetryContext]) -> None:
    from opentelemetry import context as context_api

    context_api.detach(token)


def _record_span_error(span: Span, error: BaseException) -> None:
    if not span.is_recording():
        return
    from opentelemetry.trace import Status, StatusCode

    error_type = type(error).__qualname__
    span.set_attribute("error.type", error_type)
    span.record_exception(error)
    span.set_status(Status(StatusCode.ERROR, str(error) or error_type))


def _step_attributes(
    context: Context,
    step_type: str,
    turn_id: str | None,
) -> dict[str, AttributeValue]:
    attributes: dict[str, AttributeValue] = {
        "gen_ai.conversation.id": context.flow_id,
        "session.id": context.flow_id,
        "dex.flow.id": context.flow_id,
        "dex.run.id": context.run_id,
        "dex.step.type": step_type,
        "dex.step.execution_id": context.step_execution_id,
        "dex.step.attempt": context.attempt,
    }
    if turn_id is not None:
        attributes["dex.agent.turn.id"] = turn_id
    return attributes


def _read_traceparent(context: Context) -> str | None:
    traceparent: str | None = AGENT_TRACE_CONTEXT.get(context)
    return traceparent


def _store_traceparent(
    context: Context,
    stored_traceparent: str | None,
    traceparent: str | None,
) -> None:
    if traceparent is not None:
        if traceparent != stored_traceparent:
            AGENT_TRACE_CONTEXT.set(context, traceparent)
    elif stored_traceparent is not None:
        AGENT_TRACE_CONTEXT.delete(context)


def _parent_context(traceparent: str | None) -> OpenTelemetryContext:
    from opentelemetry import trace
    from opentelemetry.context import Context as OpenTelemetryContext

    root = _parse_traceparent(traceparent)
    if root is None:
        return OpenTelemetryContext()
    return trace.set_span_in_context(trace.NonRecordingSpan(root), OpenTelemetryContext())


def _format_traceparent(span_context: SpanContext) -> str | None:
    if not span_context.is_valid:
        return None
    return (
        f"00-{span_context.trace_id:032x}-{span_context.span_id:016x}-"
        f"{int(span_context.trace_flags):02x}"
    )


def _parse_traceparent(traceparent: str | None) -> SpanContext | None:
    match = None if traceparent is None else _TRACEPARENT_PATTERN.fullmatch(traceparent)
    if match is None:
        return None
    trace_id, span_id, flags = (int(group, 16) for group in match.groups())
    if trace_id == 0 or span_id == 0:
        return None
    from opentelemetry.trace import SpanContext, TraceFlags

    return SpanContext(trace_id, span_id, is_remote=True, trace_flags=TraceFlags(flags))


def _input_messages(turn: AgentTurnContext) -> list[dict[str, Any]]:
    messages = [_chat_message(message) for message in turn.messages]
    # A resumed call also received the turn's tool results, which are not messages yet.
    for record in sorted(turn.tool_calls.values(), key=lambda record: record.deferral_order):
        messages.append(
            {
                "role": "assistant",
                "parts": [_tool_call_part(record.tool_call_id, record.tool_name, record.arguments)],
            }
        )
        messages.append(
            {
                "role": "tool",
                "parts": [
                    {
                        "type": "tool_call_response",
                        "id": record.tool_call_id,
                        "response": describe_tool_call_outcome(record),
                    }
                ],
            }
        )
    return [message for message in messages if message["parts"]]


def _first_produced_message_index(new_messages: Sequence[FrameworkMessage]) -> int:
    # new_messages spans the whole turn; this call produced only what follows the last tool result.
    return 1 + max(
        (index for index, message in enumerate(new_messages) if message.role is MessageRole.TOOL),
        default=-1,
    )


def _chat_message(message: AgentMessage | FrameworkMessage) -> dict[str, Any]:
    parts: list[dict[str, Any]] = []
    if message.text:
        parts.append(_text_part(message.text))
    if message.role is MessageRole.TOOL:
        if message.tool_output is not None:
            response: dict[str, Any] = {"type": "tool_call_response"}
            if message.tool_call_id is not None:
                response["id"] = message.tool_call_id
            response["response"] = message.tool_output
            parts.append(response)
    elif message.tool_name is not None:
        parts.append(
            _tool_call_part(message.tool_call_id, message.tool_name, message.tool_arguments)
        )
    return {"role": str(message.role), "parts": parts}


def _text_part(text: str) -> dict[str, Any]:
    return {"type": "text", "content": text}


def _tool_call_part(
    tool_call_id: str | None,
    tool_name: str,
    arguments: str | None,
) -> dict[str, Any]:
    part: dict[str, Any] = {"type": "tool_call"}
    if tool_call_id is not None:
        part["id"] = tool_call_id
    part["name"] = tool_name
    if arguments is not None:
        part["arguments"] = _json_value(arguments)
    return part


def _json_value(text: str) -> Any:
    try:
        return json.loads(text)
    except (ValueError, RecursionError):
        return text


def _to_json(value: object) -> str:
    return json.dumps(value, separators=(",", ":"), ensure_ascii=False)
