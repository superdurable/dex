# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Derive the session Flow Definition Graph from the Python core.

``dexcli visualize`` cannot read the session Flow: a Step moves through helper
methods, and the application chooses the Flow type. So this reads the Python
core itself. The registered Flow gives Steps, RPCs, persistence names, locks,
and failure routes. The source of ``session_flow.py`` gives wait conditions,
transitions, and Channel publishes. Only the words below are written by hand.

The result is the graph template that ``generate.py`` writes. Each core fills
in the Flow type and the Action permission.
"""

from __future__ import annotations

import ast
import inspect
import types
from collections.abc import Iterable, Iterator, Mapping, Sequence
from dataclasses import MISSING, dataclass, fields
from typing import Any, get_type_hints

from dex import Step, StepOptions

from dex_agents import (
    AgentSessionFlow,
    AgentToolCall,
    AnswerQuestionRequest,
    CancelTurnRequest,
    ResolveInterruptRequest,
    SteerUserMessageRequest,
    ToolCallStatus,
    ToolEffect,
    session_flow,
)

#: Replaced by the Flow type and the Action permission in each core.
FLOW_TYPE_PLACEHOLDER = "<flow-type>"
ACTION_PERMISSION_PLACEHOLDER = "<action-permission>"

#: Steps that read together on the canvas. One run of Steps, or one choice.
STEP_GROUPS: tuple[tuple[str, str, tuple[str, ...]], ...] = (
    ("session", "Session", ("StartAgentSession", "AwaitUserMessage")),
    ("model", "Model", ("RunModel", "CompactContext")),
    ("wait", "Wait", ("AwaitToolApproval", "AwaitQuestionAnswer", "AwaitTimer")),
    ("tools", "Tools", ("CallTool", "CallSingleAttemptTool", "PollToolTask")),
    ("turn-end", "Turn end", ("ResumeTurn", "ApplyTurnControl")),
    ("cancellation", "Cancellation", ("FinishTurnCancellation", "CancelToolTask")),
)


@dataclass(frozen=True)
class SessionAction:
    """An RPC that an operator can call from Dex Web, with the words Dex Web shows."""

    rpc_name: str
    label: str
    input_type: type
    field_descriptions: Mapping[str, str]


SESSION_ACTIONS = (
    SessionAction(
        "resolve_interrupt",
        "Resolve interrupt",
        ResolveInterruptRequest,
        {
            "interrupt_id": "Interrupt ID",
            "approved": "Approve the tool call",
            "rejection_message": "Message the model sees after a rejection",
        },
    ),
    SessionAction(
        "answer_question",
        "Answer question",
        AnswerQuestionRequest,
        {"interrupt_id": "Interrupt ID", "answer": "Answer"},
    ),
    SessionAction(
        "steer_user_message",
        "Steer turn",
        SteerUserMessageRequest,
        {"message_id": "Queued message ID", "text": "New message"},
    ),
    SessionAction("cancel_turn", "Stop turn", CancelTurnRequest, {"turn_id": "Turn ID"}),
)

_ACTION_VALUE_TYPES: Mapping[type, str] = {str: "string", bool: "bool", int: "int64"}
# Dex Web's index type and value type for each indexed Attribute's index type.
_INDEXED_ATTRIBUTE_TYPES: Mapping[str, tuple[str, str]] = {
    "full_text": ("fulltext", "string"),
    "datetime": ("datetime", "datetime"),
    "int": ("int", "int64"),
}
INDEXED_ATTRIBUTE_DESCRIPTIONS: Mapping[str, str] = {
    "AgentSessionTitle": "Title",
    "AgentSessionLastActivityAt": "Last activity",
    "AgentSessionTurnCount": "Turns",
}
_CHANNEL_CONDITION_METHODS = frozenset({"for_one", "for_n", "for_range", "at_least", "at_most"})
_WAIT_TYPES = {"any_of": "anyOf", "all_of": "allOf", "until": "until"}
_MOVEMENT_FAILURE_ROUTE_METHODS = frozenset(
    {"_tool_call_movement", "_tool_task_poll_movement", "_cancel_tool_task_movement"}
)

Function = ast.FunctionDef | ast.AsyncFunctionDef


@dataclass(frozen=True)
class WaitCondition:
    """One condition of a Step's WaitFor. ``channel`` is ``None`` for a Timer."""

    channel: str | None


@dataclass(frozen=True)
class StepWait:
    """What a Step's ``wait_for`` can wait on, in source order."""

    wait_type: str
    conditions: tuple[WaitCondition, ...]


class SessionFlowSource:
    """Reads what the registered Flow cannot say from the source of ``session_flow.py``."""

    def __init__(self) -> None:
        tree = ast.parse(inspect.getsource(session_flow))
        self._flow_class = next(
            node
            for node in tree.body
            if isinstance(node, ast.ClassDef) and node.name == AgentSessionFlow.__name__
        )
        self._flow_methods = _functions_of(self._flow_class.body)
        self._module_functions = _functions_of(tree.body)
        self._step_classes = {
            node.name: node
            for node in tree.body
            if isinstance(node, ast.ClassDef)
            and isinstance(getattr(session_flow, node.name), type)
            and issubclass(getattr(session_flow, node.name), Step)
        }
        self._step_attributes = self._read_step_attributes()

    def step_wait(self, step_class_name: str) -> StepWait | None:
        """Return the wait of one Step, or ``None`` when it does not define ``wait_for``."""
        methods = _functions_of(self._step_classes[step_class_name].body)
        if "wait_for" not in methods:
            return None
        conditions: list[tuple[int, WaitCondition]] = []
        wait_types: set[str] = set()
        for function in self._reachable_functions([methods["wait_for"]]):
            for call in _calls_in(function):
                name = _called_name(call)
                if _is_wait_call(call):
                    wait_types.add(_WAIT_TYPES[name])
                elif name == "_timer_until" or _is_timer_call(call):
                    conditions.append((call.lineno, WaitCondition(None)))
                elif name in _CHANNEL_CONDITION_METHODS:
                    conditions.append((call.lineno, WaitCondition(self._channel_of(call))))
        ordered = dict.fromkeys(condition for _, condition in sorted(conditions, key=_line_of))
        return StepWait(_outermost_wait_type(wait_types), tuple(ordered))

    def step_targets(self, step_class_name: str) -> tuple[list[str], list[str]]:
        """Return the Step types one Step's ``execute`` moves to, and how it ends the path."""
        methods = _functions_of(self._step_classes[step_class_name].body)
        return self._targets_of([methods["execute"]])

    def rpc_targets(self, rpc_method_name: str) -> tuple[list[str], list[str]]:
        """Return the Step types one RPC starts and the Step types it cancels."""
        function = self._flow_methods[rpc_method_name]
        targets, _ = self._targets_of([function])
        cancelled: list[str] = []
        for call in _calls_in(function):
            if _called_name(call) == "with_canceling_steps":
                cancelled.extend(
                    self._step_attributes[argument.attr]
                    for argument in call.args
                    if isinstance(argument, ast.Attribute)
                )
        return targets, cancelled

    def step_publishes(self, step_class_name: str) -> list[str]:
        """Return the Channels one Step's ``execute`` publishes to."""
        methods = _functions_of(self._step_classes[step_class_name].body)
        return self._publishes_of([methods["execute"]])

    def rpc_publishes(self, rpc_method_name: str) -> list[str]:
        """Return the Channels one RPC publishes to."""
        return self._publishes_of([self._flow_methods[rpc_method_name]])

    def check_movement_failure_routes(self) -> None:
        """Fail when a movement sets a failure route that ``movement_failure_routes`` misses."""
        routing = {
            name
            for name, function in self._flow_methods.items()
            if any(
                _called_name(call) == "on_execute_failure_proceed_to"
                for call in _calls_in(function)
            )
        }
        if routing != _MOVEMENT_FAILURE_ROUTE_METHODS:
            raise ValueError(f"movements with a failure route changed: {sorted(routing)}")

    def _read_step_attributes(self) -> dict[str, str]:
        attributes: dict[str, str] = {}
        for statement in ast.walk(self._flow_methods["__init__"]):
            if (
                isinstance(statement, ast.Assign)
                and isinstance(statement.value, ast.Call)
                and isinstance(statement.value.func, ast.Name)
                and statement.value.func.id in self._step_classes
                and isinstance(statement.targets[0], ast.Attribute)
            ):
                attributes[statement.targets[0].attr] = statement.value.func.id
        return attributes

    def _reachable_functions(self, roots: Iterable[Function]) -> Iterator[Function]:
        seen: set[str] = set()
        pending = list(roots)
        while pending:
            function = pending.pop()
            yield function
            for node in ast.walk(function):
                reached: Function | None = None
                if isinstance(node, ast.Attribute):
                    reached = self._flow_methods.get(node.attr)
                elif isinstance(node, ast.Name):
                    reached = self._module_functions.get(node.id)
                if reached is not None and reached.name not in seen:
                    seen.add(reached.name)
                    pending.append(reached)

    def _targets_of(self, roots: Iterable[Function]) -> tuple[list[str], list[str]]:
        targets: set[str] = set()
        endings: set[str] = set()
        for function in self._reachable_functions(roots):
            for call in _calls_in(function):
                name = _called_name(call)
                if name in ("dead_end", "force_fail"):
                    endings.add(name)
                elif name in ("go_to", "of") and call.args:
                    targets.update(self._step_types_of(call.args[0], function))
                elif name == "_go_to_message_step":
                    targets.update(self._step_types_of(call.args[1], function))
        return sorted(targets), sorted(endings)

    def _step_types_of(self, target: ast.expr, function: Function) -> Iterator[str]:
        if isinstance(target, ast.Attribute):
            if target.attr in self._step_attributes:
                yield self._step_attributes[target.attr]
        elif isinstance(target, ast.Name):
            for value in _assigned_values(function, target.id):
                yield from self._step_types_of(value, function)

    def _publishes_of(self, roots: Iterable[Function]) -> list[str]:
        channels: set[str] = set()
        for function in self._reachable_functions(roots):
            for call in _calls_in(function):
                if _called_name(call) == "publish":
                    channels.add(self._channel_of(call))
        return sorted(channels)

    def _channel_of(self, call: ast.Call) -> str:
        receiver = call.func.value if isinstance(call.func, ast.Attribute) else None
        if not isinstance(receiver, ast.Name):
            raise ValueError(f"Channel at line {call.lineno} is not a module constant")
        return str(getattr(session_flow, receiver.id).name)


def build_session_graph(
    flow: AgentSessionFlow, registered: Any, persistence: Sequence[Mapping[str, Any]]
) -> dict[str, Any]:
    """Return the session graph template.

    Args:
        flow: A session Flow with default options.
        registered: That Flow as its Registry registered it.
        persistence: The ``persistence`` entries of ``names.json``.

    Returns:
        A Flow Definition Graph 2.0 document with placeholders for the Flow
        type and the Action permission.
    """
    source = SessionFlowSource()
    source.check_movement_failure_routes()
    builder = _GraphBuilder(persistence)
    steps = {definition.step.get_step_type(): definition.step for definition in flow.get_steps()}
    for step_type, step in steps.items():
        builder.add_step(step_type, step, registered.steps[step_type].starting, source)
    for step_type, target_type in _failure_routes(flow, steps):
        builder.add_failure_transition(step_type, target_type)
    for rpc in sorted(registered.rpcs.values(), key=lambda entry: entry.name):
        builder.add_rpc(rpc.name, sorted(rpc.locks), source)
    return builder.finish()


class _GraphBuilder:
    def __init__(self, persistence: Sequence[Mapping[str, Any]]) -> None:
        self._nodes: list[dict[str, Any]] = []
        self._edges: list[dict[str, Any]] = []
        self._resource_ids: dict[str, str] = {}
        self._indexed_attributes: list[dict[str, str]] = []
        self._start_step_id = ""
        for entry in persistence:
            self._add_resource(entry)
            if "index" in entry:
                self._add_indexed_attribute(entry)

    def add_step(
        self, step_type: str, step: Step[Any], is_start: bool, source: SessionFlowSource
    ) -> None:
        step_id = _step_id(step_type)
        class_name = type(step).__name__
        wait = source.step_wait(class_name)
        node: dict[str, Any] = {
            "id": step_id,
            "kind": "step",
            "name": step_type,
            "phase": "execute" if wait is None else "wait_for+execute",
            "metadata": {"explanation": _explanation_of(type(step))},
        }
        if is_start:
            node["start"] = True
            self._start_step_id = step_id
        self._nodes.append(node)
        if wait is not None:
            self._add_wait(step_type, wait)
        targets, endings = source.step_targets(class_name)
        for target_type in targets:
            self._add_transition(step_id, "goTo", target_type, "execute")
        for ending in endings:
            decision_type = "deadEnd" if ending == "dead_end" else "forceFail"
            self._add_decision(step_id, decision_type, decision_type, "execute", {})
        options = step.get_step_options() or StepOptions()
        for lock in options.execute_lock_attributes:
            self._add_access("resource_lock", step_id, lock.attribute.name, "Lock", "execute")
        for channel in source.step_publishes(class_name):
            self._add_access("resource_publish", step_id, channel, "Publish", "execute")

    def add_failure_transition(self, step_type: str, target_type: str) -> None:
        self._add_edge(
            "failure_transition",
            _step_id(step_type),
            _step_id(target_type),
            "Execute failure",
            {"skipWaitFor": True},
        )

    def add_rpc(self, rpc_name: str, locks: Sequence[str], source: SessionFlowSource) -> None:
        rpc_id = f"rpc:{rpc_name}"
        self._nodes.append({"id": rpc_id, "kind": "rpc", "name": rpc_name})
        for lock in locks:
            self._add_access("resource_lock", rpc_id, lock, "Lock", None)
        for channel in source.rpc_publishes(rpc_name):
            self._add_access("resource_publish", rpc_id, channel, "Publish", None)
        targets, cancelled = source.rpc_targets(rpc_name)
        if not targets and not cancelled:
            return
        decision_id = self._add_decision(
            rpc_id,
            "rpcResult",
            "rpcResult",
            "rpc",
            {
                "cancellations": [
                    {"stepId": _step_id(step_type), "scope": "all"} for step_type in cancelled
                ]
            },
        )
        for target_type in targets:
            self._add_edge("transition", decision_id, _step_id(target_type), "GoTo", None)
        for step_type in cancelled:
            self._add_edge("cancel", decision_id, _step_id(step_type), "Cancel", None)

    def finish(self) -> dict[str, Any]:
        return {
            "schemaVersion": "2.0",
            "valid": True,
            "source": {"language": "python", "path": "dex_agents/session_flow.py"},
            "flow": {"name": FLOW_TYPE_PLACEHOLDER, "startStepId": self._start_step_id},
            "nodes": self._nodes,
            "edges": self._edges,
            "diagnostics": [],
            "groups": [
                {
                    "id": group_id,
                    "label": label,
                    "stepIds": [_step_id(step_type) for step_type in step_types],
                }
                for group_id, label, step_types in STEP_GROUPS
            ],
            "v2": {
                "indexedAttributes": self._indexed_attributes,
                "summary": {"rpcName": "GetDexSummary", "fields": []},
                "display": {"rpcName": "GetDexDisplay", "fields": []},
                "actions": [_action_definition(action) for action in SESSION_ACTIONS],
            },
        }

    def _add_resource(self, entry: Mapping[str, str]) -> None:
        kind = entry["kind"].removesuffix("_map")
        resource_id = f"resource:{kind}:{entry['name']}"
        self._resource_ids[entry["name"]] = resource_id
        resource: dict[str, Any] = {"valueType": entry["value_type"]}
        if entry["kind"].endswith("_map"):
            resource["map"] = True
        self._nodes.append(
            {"id": resource_id, "kind": kind, "name": entry["name"], "resource": resource}
        )

    def _add_indexed_attribute(self, entry: Mapping[str, Any]) -> None:
        index_type, value_type = _INDEXED_ATTRIBUTE_TYPES[entry["index"]["type"]]
        self._indexed_attributes.append(
            {
                "attributeKey": entry["name"],
                "indexKey": entry["index"]["key"],
                "indexType": index_type,
                "valueType": value_type,
                "description": INDEXED_ATTRIBUTE_DESCRIPTIONS[entry["name"]],
            }
        )

    def _add_wait(self, step_type: str, wait: StepWait) -> None:
        wait_id = f"wait:{step_type}"
        conditions: list[dict[str, Any]] = []
        for index, condition in enumerate(wait.conditions):
            if condition.channel is None:
                conditions.append({"index": index, "kind": "timer", "label": "Timer"})
                continue
            resource_id = self._resource_ids[condition.channel]
            conditions.append(
                {
                    "index": index,
                    "kind": "channel",
                    "label": condition.channel,
                    "resourceId": resource_id,
                }
            )
            self._add_edge(
                "wait_condition", resource_id, wait_id, condition.channel, {"conditionIndex": index}
            )
        self._nodes.append(
            {
                "id": wait_id,
                "kind": "wait",
                "name": wait.wait_type,
                "parentId": _step_id(step_type),
                "phase": "wait_for",
                "wait": {"type": wait.wait_type, "conditions": conditions},
            }
        )

    def _add_transition(self, owner_id: str, name: str, target_type: str, phase: str) -> None:
        decision_id = self._add_decision(owner_id, f"{name}:{target_type}", name, phase, {})
        self._add_edge("transition", decision_id, _step_id(target_type), "GoTo", None)

    def _add_decision(
        self, owner_id: str, suffix: str, decision_type: str, phase: str, details: dict[str, Any]
    ) -> str:
        decision_id = f"decision:{owner_id}:{suffix}"
        self._nodes.append(
            {
                "id": decision_id,
                "kind": "decision",
                "name": decision_type,
                "parentId": owner_id,
                "phase": phase,
                "decision": {"type": decision_type, **details},
            }
        )
        return decision_id

    def _add_access(
        self, kind: str, owner_id: str, resource_name: str, label: str, phase: str | None
    ) -> None:
        metadata = None if phase is None else {"phase": phase}
        self._add_edge(kind, owner_id, self._resource_ids[resource_name], label, metadata)

    def _add_edge(
        self, kind: str, source: str, target: str, label: str, metadata: dict[str, Any] | None
    ) -> None:
        edge: dict[str, Any] = {
            "id": f"edge:{len(self._edges) + 1:04d}",
            "kind": kind,
            "from": source,
            "to": target,
            "label": label,
        }
        if metadata is not None:
            edge["metadata"] = metadata
        self._edges.append(edge)


def _failure_routes(
    flow: AgentSessionFlow, steps: Mapping[str, Step[Any]]
) -> list[tuple[str, str]]:
    """Return each Step's failure routes: its own, then those of its movements for each effect."""
    routes: dict[tuple[str, str], None] = {}
    for step_type, step in steps.items():
        target = (step.get_step_options() or StepOptions())._execute_failure_target
        if isinstance(target, Step):
            routes[(step_type, target.get_step_type())] = None
    for effect in ToolEffect:
        tool_call = AgentToolCall(
            tool_call_id="call",
            turn_id="turn",
            tool_name="tool",
            arguments="{}",
            effect=effect,
            requires_approval=False,
            status=ToolCallStatus.APPROVED,
        )
        for movement in (
            flow._tool_call_movement(tool_call),
            flow._tool_task_poll_movement(tool_call),
            flow._cancel_tool_task_movement(tool_call),
        ):
            target = movement.options._execute_failure_target if movement.options else None
            if isinstance(target, Step) and isinstance(movement.step, Step):
                routes[(movement.step.get_step_type(), target.get_step_type())] = None
    return sorted(routes)


def _action_definition(action: SessionAction) -> dict[str, Any]:
    hints = get_type_hints(action.input_type)
    input_fields = fields(action.input_type)
    if {field.name for field in input_fields} != set(action.field_descriptions):
        raise ValueError(f"{action.rpc_name}: describe exactly the fields of its input")
    return {
        "rpcName": action.rpc_name,
        "label": action.label,
        "requiredPermission": ACTION_PERMISSION_PLACEHOLDER,
        "input": {
            "kind": "object",
            "fields": [
                {
                    "fieldName": field.name,
                    "valueType": _action_value_type(hints[field.name]),
                    "source": "user",
                    "required": field.default is MISSING,
                    "description": action.field_descriptions[field.name],
                }
                for field in input_fields
            ],
        },
    }


def _action_value_type(hint: object) -> str:
    if isinstance(hint, types.UnionType):
        (hint,) = (member for member in hint.__args__ if member is not type(None))
    return _ACTION_VALUE_TYPES[hint]  # type: ignore[index]


def _explanation_of(step_class: type) -> str:
    first_paragraph = (inspect.getdoc(step_class) or "").split("\n\n")[0]
    if not first_paragraph:
        raise ValueError(f"{step_class.__name__} needs a docstring")
    return " ".join(first_paragraph.split()).replace("``", "")


def _step_id(step_type: str) -> str:
    return f"step:{step_type}"


def _functions_of(body: Iterable[ast.stmt]) -> dict[str, Function]:
    return {
        node.name: node for node in body if isinstance(node, ast.FunctionDef | ast.AsyncFunctionDef)
    }


def _calls_in(function: Function) -> Iterator[ast.Call]:
    return (node for node in ast.walk(function) if isinstance(node, ast.Call))


def _called_name(call: ast.Call) -> str:
    if isinstance(call.func, ast.Attribute):
        return call.func.attr
    return call.func.id if isinstance(call.func, ast.Name) else ""


def _is_wait_call(call: ast.Call) -> bool:
    return _is_called_on(call, "Wait") and _called_name(call) in _WAIT_TYPES


def _is_timer_call(call: ast.Call) -> bool:
    return _is_called_on(call, "Timer")


def _is_called_on(call: ast.Call, receiver_name: str) -> bool:
    return (
        isinstance(call.func, ast.Attribute)
        and isinstance(call.func.value, ast.Name)
        and call.func.value.id == receiver_name
    )


def _outermost_wait_type(wait_types: set[str]) -> str:
    for wait_type in ("anyOf", "allOf", "until"):
        if wait_type in wait_types:
            return wait_type
    raise ValueError("a wait_for that returns no Wait")


def _assigned_values(function: Function, name: str) -> Iterator[ast.expr]:
    for node in ast.walk(function):
        value: ast.expr | None = None
        target: ast.expr | None = None
        if isinstance(node, ast.Assign):
            target, value = node.targets[0], node.value
        elif isinstance(node, ast.AnnAssign):
            target, value = node.target, node.value
        if isinstance(target, ast.Name) and target.id == name and value is not None:
            yield value


def _line_of(entry: tuple[int, WaitCondition]) -> int:
    return entry[0]
