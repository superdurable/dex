# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Plain Python functions as Stepwise tools, with schemas derived from type hints."""

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

from dex_agents.events import AgentEventWriter
from dex_agents.runner import ToolExecutionError, ToolInputRequested, ToolPolicy
from dex_agents.stepwise.model import ModelToolDefinition
from dex_agents.values import QuestionAnswer, ToolEffect


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


@dataclass(frozen=True, kw_only=True)
class FunctionTool:
    """Run one plain Python function as a Stepwise tool in its own Step.

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
    """

    name: str
    description: str
    parameters: Mapping[str, Any]
    effect: ToolEffect
    requires_approval: bool | None
    function: Callable[..., Any]
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

    async def invoke(self, arguments: str, context: ToolCallContext) -> str | ToolInputRequested:
        """Check the arguments against the schema and call the function once.

        A synchronous function runs in a worker thread. A ``str`` result is the
        output. A ``ToolInputRequested`` result passes through. Any other
        result is serialized as JSON; dataclasses become objects and enum
        members become their values.

        Args:
            arguments: Tool arguments as JSON text.
            context: Identity of the call.

        Returns:
            The output text, or the function's ``ToolInputRequested``.

        Raises:
            ToolExecutionError: If the arguments break the schema, or the
                function raised it. The model sees the message.
            Exception: Anything else the function raises fails the Step attempt.
        """
        values = _parse_arguments(self.name, arguments)
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
        if isinstance(result, ToolInputRequested | str):
            return result
        return json.dumps(_json_value(result), ensure_ascii=False)


def function_tool(
    *,
    effect: ToolEffect,
    requires_approval: bool | None = None,
    name: str | None = None,
    description: str | None = None,
    parameters_schema: Mapping[str, Any] | None = None,
) -> Callable[[Callable[..., Any]], FunctionTool]:
    """Turn a plain sync or async function into a Stepwise tool.

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

    Args:
        effect: What the tool does outside the session.
        requires_approval: Whether each call waits for approval. ``None`` uses
            the effect's default.
        name: Tool name; defaults to the function name.
        description: Model-visible description; defaults to the docstring.
        parameters_schema: JSON Schema object of the arguments; defaults to
            the schema derived from the type hints.

    Returns:
        A decorator that returns the ``FunctionTool``.

    Raises:
        TypeError: From the decorator, if a parameter type is unsupported and
            no ``parameters_schema`` is given, or a parameter is positional-only
            or variadic.
    """

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


def _parse_arguments(tool_name: str, arguments: str) -> dict[str, Any]:
    try:
        values = json.loads(arguments or "{}")
    except json.JSONDecodeError:
        # A fixed text: parser messages differ between languages and versions.
        raise ToolExecutionError(
            f"invalid arguments for {tool_name}: arguments are not valid JSON"
        ) from None
    if not isinstance(values, dict):
        raise ToolExecutionError(f"invalid arguments for {tool_name}: expected a JSON object")
    return values


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
