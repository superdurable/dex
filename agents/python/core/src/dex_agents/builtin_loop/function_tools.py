# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Plain Python functions as built-in agent loop tools, with schemas derived from type hints."""

from __future__ import annotations

import asyncio
import dataclasses
import enum
import inspect
import json
import math
import types
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from typing import (
    Annotated,
    Any,
    Literal,
    Union,
    get_args,
    get_origin,
    get_type_hints,
    is_typeddict,
)

from dex_agents.builtin_loop.model import ModelToolDefinition
from dex_agents.events import AgentEventWriter
from dex_agents.media import MediaStore
from dex_agents.runner import ToolExecutionError, ToolInputRequested, ToolPolicy
from dex_agents.values import AgentImageReference, QuestionAnswer, ToolEffect


@dataclass(frozen=True, kw_only=True)
class ToolCallContext:
    """Give a function tool the identity of its call and the session's live writers.

    Declare a parameter typed ``ToolCallContext`` in a function tool to receive
    it. That parameter is not part of the tool's schema, so the model never
    sees it.

    Example:
        >>> @function_tool(effect=ToolEffect.NON_IDEMPOTENT_WRITE)
        ... async def refund_order(order_id: str, context: ToolCallContext) -> str:
        ...     \"\"\"Refund an order.\"\"\"
        ...     return await payments.refund(order_id, idempotency_key=context.idempotency_key)

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        turn_id: ``"turn-<n>"`` of the running turn.
        tool_call_id: The tool call this run answers.
        idempotency_key: ``"<flow_id>/<turn_id>/<tool_call_id>"``. It is the
            same on every attempt. Pass it to the external system when it
            supports idempotency.
        attempt: One-based Step attempt.
        events: Writer for live events, bound to the tool call Step attempt.
            It is safe to call from a worker thread.
        heartbeat: Coroutine function that records Step liveness. The session
            already heartbeats in the background.
        input_answers: Answers to the questions this call asked by returning
            ``ToolInputRequested``, oldest first. Empty on the first run.
        input_continuation: ``continuation`` of the last ``ToolInputRequested``,
            or ``None``.
        effect: Effect of this call after the tool policies are applied. It
            is the effect stored on the call, which chose ``CallTool`` or
            ``CallSingleAttemptTool`` and the default approval. It can differ
            from the tool's own effect when a policy in
            ``AgentDefinition.tool_policies`` or a session policy overrides
            it. The runner always sets it. A context built by hand, such as
            in a unit test, defaults to ``UNCLASSIFIED``, an unknown effect;
            handle it as a write whose outcome may be unknown. Use it in a
            wrapper that turns errors into ``ToolExecutionError``.
            ``NON_IDEMPOTENT_WRITE`` and ``UNCLASSIFIED`` calls run once in
            ``CallSingleAttemptTool``. For them, let an error whose outcome
            is unknown propagate, so the session records ``outcome_unknown``
            and never repeats the call::

                except Exception as error:
                    if context.effect not in (ToolEffect.READ, ToolEffect.IDEMPOTENT_WRITE):
                        raise
                    raise ToolExecutionError(str(error)) from error

            .. versionadded:: 0.5.0
        media_store: The store that holds the session's images, the one the
            runner was built with, or ``None`` when it has none. Upload image
            bytes with ``put_image`` and return the references in a
            ``FunctionToolResult``. See ``FunctionToolResult`` for an example.

            .. versionadded:: 0.5.0
    """

    flow_id: str
    turn_id: str
    tool_call_id: str
    idempotency_key: str
    attempt: int
    events: AgentEventWriter
    heartbeat: Callable[[], Awaitable[None]]
    input_answers: Sequence[QuestionAnswer] = ()
    input_continuation: str | None = None
    effect: ToolEffect = ToolEffect.UNCLASSIFIED
    media_store: MediaStore | None = None


@dataclass(frozen=True, kw_only=True)
class FunctionToolResult:
    """Return text and images from a function tool.

    Return it from a function tool to show the model images with the output.
    Upload each image's bytes with ``ToolCallContext.media_store`` inside the
    call, then return the references. A retried attempt uploads the same
    bytes to the same object, so uploading again is safe.

    The runner returns the result as ``ToolExecutionResult(output, images)``,
    so the session checks the images exactly as it checks a tool source's
    images. More than ``MAX_IMAGES_PER_MESSAGE`` images, or a reference that
    ``validate_image_reference`` refuses, fails the attempt with
    ``ImageValidationError``. A failed attempt of a ``READ`` or
    ``IDEMPOTENT_WRITE`` tool retries; a failed single-attempt call records
    ``outcome_unknown``. Images from a runner without a media store raise
    ``ToolExecutionError`` instead, because no retry could show them: the
    session records ``FAILED`` once and the model sees the error text. A
    result without images is the same as returning ``output`` as a ``str``.

    Example:
        >>> @function_tool(effect=ToolEffect.READ)
        ... async def take_screenshot(context: ToolCallContext) -> str | FunctionToolResult:
        ...     \"\"\"Take a screenshot of the screen.\"\"\"
        ...     if context.media_store is None:
        ...         return "Screenshots need a media store."
        ...     reference = await context.media_store.put_image(await screen.capture_png())
        ...     return FunctionToolResult(output="Took a screenshot.", images=[reference])

    Attributes:
        output: Tool output text the model sees.
        images: Images the model sees after ``output``, uploaded with
            ``MediaStore.put_image``, at most ``MAX_IMAGES_PER_MESSAGE``.
            Defaults to none.

    .. versionadded:: 0.5.0
    """

    output: str
    images: Sequence[AgentImageReference] = ()


@dataclass(frozen=True, kw_only=True)
class FunctionTool:
    """Run one plain Python function as a built-in agent loop tool in its own Step.

    Create it with ``@function_tool``. The effect picks the tool call Step and
    the default approval, exactly as ``ToolPolicy`` does for adapter tools.

    Attributes:
        name: Tool name the model calls.
        description: Model-visible description.
        parameters: JSON Schema object of the arguments.
        effect: What the tool does outside the session.
        requires_approval: Whether each call waits for ``resolve_interrupt``,
            or ``None`` for the effect's default.
        function: The wrapped function.
        prepare_arguments: Converts the model's decoded arguments before the
            schema check, or ``None`` to check them unchanged. See
            ``function_tool``.

            .. versionadded:: 0.5.0
    """

    name: str
    description: str
    parameters: Mapping[str, Any]
    effect: ToolEffect
    requires_approval: bool | None
    function: Callable[..., Any]
    prepare_arguments: Callable[[Any], Any] | None = None
    _context_parameter: str | None = None
    _parameter_types: Mapping[str, Any] = dataclasses.field(default_factory=dict)

    @property
    def policy(self) -> ToolPolicy:
        """Return the tool's own policy.

        Returns:
            ``ToolPolicy(effect, requires_approval)`` with the effect's default
            approval resolved.
        """
        policy = ToolPolicy(self.effect, self.requires_approval)
        return ToolPolicy(policy.effect, policy.is_approval_required)

    @property
    def definition(self) -> ModelToolDefinition:
        """Return the definition the runner offers to the model.

        Returns:
            The name, description, and parameter schema.
        """
        return ModelToolDefinition(
            name=self.name, description=self.description, parameters=self.parameters
        )

    async def invoke(
        self, arguments: str, context: ToolCallContext
    ) -> str | FunctionToolResult | ToolInputRequested:
        """Convert and check the arguments, then call the function once.

        ``prepare_arguments``, when set, converts the decoded arguments before
        the schema check.

        A synchronous function runs in a worker thread. A ``str`` result is the
        output. A ``FunctionToolResult`` or ``ToolInputRequested`` result
        passes through. Any other result is serialized as JSON; dataclasses
        become objects and enum members become their values.

        .. versionchanged:: 0.5.0
            A ``FunctionToolResult`` result passes through.

        Args:
            arguments: Tool arguments as JSON text.
            context: Identity of the call.

        Returns:
            The output text, the function's ``FunctionToolResult``, or its
            ``ToolInputRequested``.

        Raises:
            ToolExecutionError: If the arguments break the schema,
                ``prepare_arguments`` raised an exception or returned an
                awaitable, or the function raised it. The model sees the
                message.
            asyncio.CancelledError: If ``prepare_arguments`` raised it; it
                passes through unchanged.
            Exception: Anything else the function raises fails the Step attempt.
        """
        values = _prepare_arguments(self.name, arguments, self.prepare_arguments)
        problem = _schema_violation(values, self.parameters, "arguments")
        if problem is not None:
            raise ToolExecutionError(f"invalid arguments for {self.name}: {problem}")
        call_arguments = {
            name: _convert_argument(values[name], self._parameter_types[name])
            for name in self._parameter_types
            if name in values
        }
        if self._context_parameter is not None:
            call_arguments[self._context_parameter] = context
        if inspect.iscoroutinefunction(self.function):
            result = await self.function(**call_arguments)
        else:
            result = await asyncio.to_thread(self.function, **call_arguments)
        if isinstance(result, ToolInputRequested | FunctionToolResult | str):
            return result
        return json.dumps(_json_value(result), ensure_ascii=False)


def function_tool(
    *,
    effect: ToolEffect,
    requires_approval: bool | None = None,
    name: str | None = None,
    description: str | None = None,
    parameters_schema: Mapping[str, Any] | None = None,
    prepare_arguments: Callable[[Any], Any] | None = None,
) -> Callable[[Callable[..., Any]], FunctionTool]:
    """Turn a plain sync or async function into a built-in agent loop tool.

    The name defaults to the function name and the description to its
    docstring. The parameter schema is derived from the type hints, using the
    standard library only. Supported types are ``str``, ``int``, ``float``,
    ``bool``, ``None``, ``list``, ``dict`` with ``str`` keys, ``Literal``,
    ``Enum``, ``Optional`` and other unions, dataclasses, ``TypedDict``, and
    ``Annotated[T, "description"]``. Pass ``parameters_schema`` to give the
    schema yourself; the arguments are then passed as decoded JSON. A
    parameter typed ``ToolCallContext`` receives the call's context and is not
    in the schema. A parameter without a default is required.

    Every tool declares its effect. ``READ`` and ``IDEMPOTENT_WRITE`` tools run
    in ``CallTool`` with up to three attempts; other effects run once in
    ``CallSingleAttemptTool`` and need approval by default.

    Example:
        >>> @function_tool(effect=ToolEffect.READ)
        ... async def lookup_order(order_id: Annotated[str, "The order ID"]) -> dict[str, str]:
        ...     \"\"\"Look up an order by its ID.\"\"\"
        ...     return await orders.get(order_id)

        A tool whose argument shape changed accepts the old shape with
        ``prepare_arguments``:

        >>> def accept_version_one(arguments: Any) -> Any:
        ...     # Version 1 took {"id"}; version 2 takes {"order_id"}.
        ...     if isinstance(arguments, dict) and "id" in arguments:
        ...         return {"order_id": arguments["id"]}
        ...     return arguments
        >>> @function_tool(effect=ToolEffect.READ, prepare_arguments=accept_version_one)
        ... async def get_order(order_id: str) -> str:
        ...     \"\"\"Get an order by its ID.\"\"\"
        ...     return await orders.get(order_id)

    Args:
        effect: What the tool does outside the session.
        requires_approval: Whether each call waits for approval. ``None`` uses
            the effect's default.
        name: Tool name; defaults to the function name.
        description: Model-visible description; defaults to the docstring.
        parameters_schema: JSON Schema object of the arguments; defaults to
            the schema derived from the type hints.
        prepare_arguments: Converts the model's arguments before the schema
            check, such as an argument shape that an earlier version of the
            tool accepted. It receives the arguments decoded from JSON, which
            may be any JSON value, and returns the arguments to check and pass
            to the function. Any exception it raises fails the call like a
            schema violation: the model sees ``invalid arguments for <tool>:
            <message>``, and the function does not run. It must be
            synchronous and deterministic, and it must have no side effects,
            because every attempt converts the stored arguments again. An
            awaitable result, such as from an ``async def`` function, fails
            the call with ``invalid arguments for <tool>: argument conversion
            must be synchronous``; a coroutine is closed without running, and
            a Future or Task is cancelled. The stored tool call, the approval
            interrupt, and the transcript keep the model's original arguments.
            Defaults to none.

            .. versionadded:: 0.5.0

    Returns:
        A decorator that returns the ``FunctionTool``.

    Raises:
        TypeError: If ``prepare_arguments`` is not callable, or, from the
            decorator, if a parameter type is unsupported and no
            ``parameters_schema`` is given, or a parameter is positional-only
            or variadic.
    """
    if prepare_arguments is not None and not callable(prepare_arguments):
        raise TypeError("function tool prepare_arguments must be callable")

    def decorate(function: Callable[..., Any]) -> FunctionTool:
        tool_name = name or function.__name__
        hints = get_type_hints(function, include_extras=True)
        context_parameter: str | None = None
        parameter_types: dict[str, Any] = {}
        properties: dict[str, Any] = {}
        required: list[str] = []
        for parameter in inspect.signature(function).parameters.values():
            if parameter.kind not in (
                inspect.Parameter.POSITIONAL_OR_KEYWORD,
                inspect.Parameter.KEYWORD_ONLY,
            ):
                raise TypeError(f"tool {tool_name} parameter {parameter.name} must be a keyword")
            annotation = hints.get(parameter.name, Any)
            if annotation is ToolCallContext:
                context_parameter = parameter.name
                continue
            parameter_types[parameter.name] = annotation
            if parameters_schema is None:
                properties[parameter.name] = _schema_for_type(annotation, tool_name)
            if parameter.default is inspect.Parameter.empty:
                required.append(parameter.name)
        schema: Mapping[str, Any] = parameters_schema or {
            "type": "object",
            "properties": properties,
            "required": required,
            "additionalProperties": False,
        }
        if parameters_schema is not None:
            parameter_types = {parameter: Any for parameter in parameter_types}
        return FunctionTool(
            name=tool_name,
            description=description or inspect.getdoc(function) or "",
            parameters=schema,
            effect=effect,
            requires_approval=requires_approval,
            function=function,
            prepare_arguments=prepare_arguments,
            _context_parameter=context_parameter,
            _parameter_types=parameter_types,
        )

    return decorate


def _schema_for_type(annotation: Any, tool_name: str) -> dict[str, Any]:
    origin = get_origin(annotation)
    arguments = get_args(annotation)
    if origin is Annotated:
        schema = _schema_for_type(arguments[0], tool_name)
        descriptions = [metadata for metadata in arguments[1:] if isinstance(metadata, str)]
        return {**schema, "description": descriptions[0]} if descriptions else schema
    if annotation is Any:
        return {}
    if annotation is None or annotation is type(None):
        return {"type": "null"}
    if annotation is bool:
        return {"type": "boolean"}
    if annotation is int:
        return {"type": "integer"}
    if annotation is float:
        return {"type": "number"}
    if annotation is str:
        return {"type": "string"}
    if origin is Literal:
        return {"enum": list(arguments)}
    if origin in (Union, types.UnionType):
        return {"anyOf": [_schema_for_type(member, tool_name) for member in arguments]}
    if annotation is list or origin in (list, Sequence):
        items = _schema_for_type(arguments[0], tool_name) if arguments else {}
        return {"type": "array", "items": items}
    if annotation is dict or origin in (dict, Mapping):
        if arguments and arguments[0] is not str:
            raise TypeError(f"tool {tool_name}: dict keys must be str")
        values = _schema_for_type(arguments[1], tool_name) if arguments else {}
        return {"type": "object", "additionalProperties": values}
    if isinstance(annotation, type) and issubclass(annotation, enum.Enum):
        return {"enum": [member.value for member in annotation]}
    if isinstance(annotation, type) and dataclasses.is_dataclass(annotation):
        hints = get_type_hints(annotation, include_extras=True)
        fields = dataclasses.fields(annotation)
        return {
            "type": "object",
            "properties": {
                item.name: _schema_for_type(hints[item.name], tool_name) for item in fields
            },
            "required": [
                item.name
                for item in fields
                if item.default is dataclasses.MISSING
                and item.default_factory is dataclasses.MISSING
            ],
            "additionalProperties": False,
        }
    if is_typeddict(annotation):
        hints = get_type_hints(annotation, include_extras=True)
        return {
            "type": "object",
            "properties": {key: _schema_for_type(hint, tool_name) for key, hint in hints.items()},
            "required": sorted(annotation.__required_keys__),
            "additionalProperties": False,
        }
    raise TypeError(
        f"tool {tool_name}: type {annotation!r} has no derived schema; pass parameters_schema"
    )


def _prepare_arguments(
    tool_name: str, arguments: str, prepare_arguments: Callable[[Any], Any] | None
) -> dict[str, Any]:
    try:
        values = json.loads(arguments or "{}")
    except json.JSONDecodeError:
        # A fixed text: parser messages differ between languages and versions.
        raise ToolExecutionError(
            f"invalid arguments for {tool_name}: arguments are not valid JSON"
        ) from None
    if prepare_arguments is not None:
        try:
            values = prepare_arguments(values)
        except Exception as error:
            # Any error, so a failed conversion never retries or records outcome_unknown.
            raise ToolExecutionError(f"invalid arguments for {tool_name}: {error}") from error
        if inspect.isawaitable(values):
            _discard_refused_awaitable(values)
            raise ToolExecutionError(
                f"invalid arguments for {tool_name}: argument conversion must be synchronous"
            )
    if not isinstance(values, dict):
        raise ToolExecutionError(f"invalid arguments for {tool_name}: expected a JSON object")
    return values


def _discard_refused_awaitable(awaitable: Awaitable[Any]) -> None:
    if inspect.iscoroutine(awaitable):
        # Closing it avoids the "never awaited" warning; it never ran.
        awaitable.close()
    elif asyncio.isfuture(awaitable):
        # Cancel a running Task, and read the outcome so asyncio never logs it as unretrieved.
        awaitable.cancel()
        awaitable.add_done_callback(_retrieve_refused_future_outcome)


def _retrieve_refused_future_outcome(future: asyncio.Future[Any]) -> None:
    if not future.cancelled():
        # The conversion was refused; its exception only needs marking as retrieved.
        future.exception()


_JSON_TYPE_CHECKS: Mapping[str, Callable[[object], bool]] = {
    "string": lambda value: isinstance(value, str),
    "boolean": lambda value: isinstance(value, bool),
    "null": lambda value: value is None,
    "array": lambda value: isinstance(value, list),
    "object": lambda value: isinstance(value, dict),
    "number": lambda value: isinstance(value, int | float) and not isinstance(value, bool),
    "integer": lambda value: (
        isinstance(value, int | float)
        and not isinstance(value, bool)
        and math.isfinite(value)
        and value == int(value)
    ),
}


def _schema_violation(value: object, schema: Mapping[str, Any], path: str) -> str | None:
    """Return the first way ``value`` breaks a JSON Schema subset, or ``None``."""
    if "anyOf" in schema:
        if all(_schema_violation(value, option, path) for option in schema["anyOf"]):
            return f"{path} matches none of the allowed types"
    expected = schema.get("type")
    if expected is not None:
        expected_types = [expected] if isinstance(expected, str) else list(expected)
        if not any(_JSON_TYPE_CHECKS[name](value) for name in expected_types):
            return f"{path} must be {' or '.join(expected_types)}"
    if "enum" in schema and value not in schema["enum"]:
        return f"{path} must be one of {json.dumps(schema['enum'], ensure_ascii=False)}"
    if isinstance(value, dict):
        return _object_violation(value, schema, path)
    if isinstance(value, list) and isinstance(schema.get("items"), Mapping):
        for index, item in enumerate(value):
            problem = _schema_violation(item, schema["items"], f"{path}[{index}]")
            if problem is not None:
                return problem
    if isinstance(value, int | float) and not isinstance(value, bool):
        if "minimum" in schema and value < schema["minimum"]:
            return f"{path} must be at least {schema['minimum']}"
        if "maximum" in schema and value > schema["maximum"]:
            return f"{path} must be at most {schema['maximum']}"
    return None


def _object_violation(value: dict[str, Any], schema: Mapping[str, Any], path: str) -> str | None:
    properties: Mapping[str, Any] = schema.get("properties", {})
    for key in schema.get("required", []):
        if key not in value:
            return f"{path}.{key} is required"
    additional = schema.get("additionalProperties", True)
    for key, item in value.items():
        if key in properties:
            problem = _schema_violation(item, properties[key], f"{path}.{key}")
        elif additional is False:
            problem = f"{path}.{key} is not allowed"
        elif isinstance(additional, Mapping):
            problem = _schema_violation(item, additional, f"{path}.{key}")
        else:
            problem = None
        if problem is not None:
            return problem
    return None


def _convert_argument(value: Any, annotation: Any) -> Any:
    origin = get_origin(annotation)
    arguments = get_args(annotation)
    if origin is Annotated:
        return _convert_argument(value, arguments[0])
    if value is None:
        return None
    if origin in (Union, types.UnionType):
        for member in arguments:
            if member is not type(None) and not _schema_violation(
                value, _schema_for_type(member, "argument"), "value"
            ):
                return _convert_argument(value, member)
        return value
    if annotation is float and isinstance(value, int):
        return float(value)
    if annotation is int and isinstance(value, float):
        return int(value)
    if isinstance(annotation, type) and issubclass(annotation, enum.Enum):
        return annotation(value)
    if isinstance(annotation, type) and dataclasses.is_dataclass(annotation):
        hints = get_type_hints(annotation, include_extras=True)
        return annotation(
            **{key: _convert_argument(item, hints[key]) for key, item in value.items()}
        )
    if (annotation is list or origin in (list, Sequence)) and arguments:
        return [_convert_argument(item, arguments[0]) for item in value]
    if (annotation is dict or origin in (dict, Mapping)) and len(arguments) == 2:
        return {key: _convert_argument(item, arguments[1]) for key, item in value.items()}
    return value


def _json_value(value: Any) -> Any:
    if dataclasses.is_dataclass(value) and not isinstance(value, type):
        return {
            item.name: _json_value(getattr(value, item.name)) for item in dataclasses.fields(value)
        }
    if isinstance(value, enum.Enum):
        return _json_value(value.value)
    if isinstance(value, Mapping):
        return {str(key): _json_value(item) for key, item in value.items()}
    if isinstance(value, list | tuple):
        return [_json_value(item) for item in value]
    return value
