# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Declare Dex Web Actions on RPCs and project their Work Queue permissions."""

from __future__ import annotations

import json
import re
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from math import isfinite
from typing import Any, Generic, TypeVar

from dex._value_mapper import ValueMapper
from dex.attribute import Attribute
from dex.attribute_match import AttributeMatch
from dex.codec import CodecRegistry
from dex.dexpb import dex_pb2 as pb
from dex.runtime_errors import ValueMappingError

ValueT = TypeVar("ValueT")

WORK_QUEUE_PERMISSIONS_INDEX_KEY = "DexWorkQueuePermissions"
"""The KeywordList Search Attribute that holds a Flow execution's Action permissions.

The Server maintains it from the Action permission mappings that Workers send.
It lists every permission whose Action has been available during the current
Flow execution. It is a Work Queue discovery projection, not an authorization
result: the RPC gateway must still authorize each Action against its required
permission.

.. versionadded:: 1.39.0
"""

_ACTION_PERMISSION_PATTERN = re.compile(r"^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$")
_ACTION_SCALAR_KINDS = frozenset({"string_value", "bool_value", "int_value", "double_value"})


@dataclass(frozen=True, init=False)
class ActionCondition(Generic[ValueT]):
    """Describe when an Action is available, from one scalar Attribute.

    Create a condition with :func:`when_attribute_matches` and pass it to
    :class:`Action`. The Action is available while the Attribute equals any of
    the condition's values. A missing or deleted Attribute does not match.

    The condition decides two things. Dex Web offers the Action only while it
    matches, and the Worker projects the Action's permission into
    ``DexWorkQueuePermissions`` when it first matches. It does not authorize the
    RPC: the handler must check current state again before it changes anything.

    .. versionadded:: 1.39.0

    Attributes:
        attribute: The scalar Attribute the condition reads.
        matches: The equality matches, combined with OR.
    """

    attribute: Attribute[ValueT]
    matches: tuple[AttributeMatch[ValueT], ...]

    def __init__(
        self,
        attribute: Attribute[ValueT],
        matches: Sequence[AttributeMatch[ValueT]],
    ) -> None:
        """Create a condition; prefer :func:`when_attribute_matches`.

        Args:
            attribute: The scalar Attribute the condition reads.
            matches: The equality matches, combined with OR.
        """
        object.__setattr__(self, "attribute", attribute)
        object.__setattr__(self, "matches", tuple(matches))


def when_attribute_matches(
    attribute: Attribute[ValueT],
    *matches: AttributeMatch[ValueT],
) -> ActionCondition[ValueT]:
    """Create an Action condition on one scalar Attribute.

    At least one match is required, and every match must be created with
    :meth:`AttributeMatch.equal_to`. Several matches use OR semantics. A missing
    or deleted Attribute does not match. Supported value types are ``str``,
    ``bool``, ``int``, and finite ``float``, or any type whose codec encodes to
    one of those scalars.

    The Attribute must be declared in the ``PersistenceSchema`` of the Flow that
    registers the Action, and must not set ``hard_delete_on_null``. Registry
    construction checks these rules; this function only records its arguments.

    .. versionadded:: 1.39.0

    Args:
        attribute: The scalar Attribute the condition reads.
        *matches: One or more :meth:`AttributeMatch.equal_to` values.

    Returns:
        A condition to pass as ``Action(condition=...)``.

    Examples:
        >>> case_status = Attribute("case-status", str)
        >>> awaiting_manager = when_attribute_matches(
        ...     case_status, AttributeMatch.equal_to("awaiting-manager")
        ... )
    """
    return ActionCondition(attribute, matches)


@dataclass(frozen=True, init=False)
class Action:
    """Declare an RPC as an Action that a person can invoke from Dex Web.

    Pass an Action to :func:`dex.rpc` with ``action=``. Dex Web shows the label as
    a button or form on a run while the condition matches, and ``Working as``
    in Work Queue lists the run for the required permission. ``dexcli visualize``
    reads the declaration from source into the Flow Definition Graph, so write
    the label, the permission, and the condition values as literals or
    module-level constants.

    The Worker sends the Action permission mappings of every Action with a
    condition whenever a handler writes a condition Attribute. The Server then
    adds each newly matching permission to ``DexWorkQueuePermissions``. A Client
    or SubFlow start also writes the permissions whose conditions already match
    the initial Attributes.

    The condition is optional. An Action without one is offered on every active
    run and has no permission mapping. Use it for an RPC that checks current
    state itself. Its permission reaches ``DexWorkQueuePermissions`` only
    through another Action with a condition.

    An Action never authorizes a call. Dex Web checks the permission it is
    given, and the handler must read current state again, under a lock when the
    check and the effect must be atomic.

    An Action RPC takes no input or one dataclass input. Describe each input
    field for the Dex Web form with a ``# dex:input`` comment directive directly
    above the RPC's decorators; see the Dex Web reference for the directive.

    .. versionadded:: 1.39.0

    Attributes:
        label: The user-facing Action label, such as ``"Approve refund"``.
        required_permission: The one permission the Action requires. It uses
            lowercase letter and number segments separated by dots or hyphens,
            such as ``"refund.approve"``. Several Actions may share a permission.
        condition: When the Action is available, or ``None`` for always while
            the run is active.

    Examples:
        >>> case_status = Attribute("case-status", str)
        >>> class RefundFlow(Flow[None]):
        ...     def get_persistence_schema(self) -> PersistenceSchema:
        ...         return PersistenceSchema.of(case_status)
        ...
        ...     # dex:input field-name:reason value-type:string source:user required:true description:"Reason"
        ...     @rpc(
        ...         lock_attributes=(case_status.lock(),),
        ...         action=Action(
        ...             "Approve refund",
        ...             required_permission="refund.approve",
        ...             condition=when_attribute_matches(
        ...                 case_status, AttributeMatch.equal_to("awaiting-manager")
        ...             ),
        ...         ),
        ...     )
        ...     def approve_refund(self, context: Context, request: ApproveRequest) -> None:
        ...         if case_status.get(context) != "awaiting-manager":
        ...             raise ValueError("the case is no longer awaiting approval")
        ...         case_status.set(context, "approved")
    """

    label: str
    required_permission: str
    condition: ActionCondition[Any] | None

    def __init__(
        self,
        label: str,
        *,
        required_permission: str,
        condition: ActionCondition[Any] | None = None,
    ) -> None:
        """Create an Action declaration.

        Registry construction validates it, not this constructor.

        Args:
            label: The non-blank user-facing label.
            required_permission: The one permission the Action requires.
            condition: An optional :func:`when_attribute_matches` condition.
        """
        object.__setattr__(self, "label", label)
        object.__setattr__(self, "required_permission", required_permission)
        object.__setattr__(self, "condition", condition)


@dataclass(frozen=True)
class _RegisteredAction:
    rpc_name: str
    label: str
    required_permission: str
    attribute_name: str | None
    operands: tuple[pb.Value, ...]

    def matches(self, value: pb.Value | None) -> bool:
        if value is None or value.WhichOneof("kind") in (None, "null_value"):
            return False
        return any(_equal_action_scalar_values(value, operand) for operand in self.operands)


def _compile_action(
    rpc_name: str,
    action: object,
    persistence: Mapping[str, object],
    codec_registry: CodecRegistry,
) -> _RegisteredAction:
    if not isinstance(action, Action):
        raise TypeError(f"RPC {rpc_name} Action must be a dex.Action")
    if not isinstance(action.label, str) or not action.label.strip():
        raise ValueError(f"RPC {rpc_name} Action label must not be empty")
    permission = action.required_permission
    if not isinstance(permission, str) or not _ACTION_PERMISSION_PATTERN.match(permission):
        raise ValueError(
            f"RPC {rpc_name} Action permission {permission!r} must use lowercase letter "
            "and number segments separated by dots or hyphens"
        )
    condition = action.condition
    if condition is None:
        return _RegisteredAction(rpc_name, action.label, permission, None, ())
    if not isinstance(condition, ActionCondition):
        raise TypeError(
            f"RPC {rpc_name} Action condition must come from when_attribute_matches"
        )
    attribute = condition.attribute
    if not isinstance(attribute, Attribute):
        raise TypeError(
            f"RPC {rpc_name} Action condition requires a scalar Attribute, not an AttributeMap"
        )
    registered = persistence.get(attribute.name)
    if not isinstance(registered, Attribute):
        raise ValueError(
            f"RPC {rpc_name} Action condition Attribute {attribute.name!r} "
            "is not declared by the Flow"
        )
    if registered.hard_delete_on_null:
        raise ValueError(
            f"RPC {rpc_name} Action condition Attribute {attribute.name!r} uses "
            "hard_delete_on_null, which conditions cannot read"
        )
    if registered != attribute:
        raise ValueError(
            f"RPC {rpc_name} Action condition Attribute {attribute.name!r} does not "
            "match its registered definition"
        )
    if not condition.matches:
        raise ValueError(
            f"RPC {rpc_name} Action condition Attribute {attribute.name!r} "
            "requires at least one match"
        )
    values = ValueMapper(codec_registry)
    codec = codec_registry.resolve(attribute.value_type)
    operands: list[pb.Value] = []
    for match in condition.matches:
        if (
            not isinstance(match, AttributeMatch)
            or match._operator != pb.ATTRIBUTE_MATCH_OPERATOR_EQUAL
        ):
            raise ValueError(
                f"RPC {rpc_name} Action condition Attribute {attribute.name!r} "
                "supports only AttributeMatch.equal_to"
            )
        try:
            operand = values.encode(match._operand, codec)
        except ValueMappingError as error:
            raise ValueError(
                f"RPC {rpc_name} Action condition Attribute {attribute.name!r}: {error}"
            ) from error
        kind = operand.WhichOneof("kind")
        if kind not in _ACTION_SCALAR_KINDS:
            raise ValueError(
                f"RPC {rpc_name} Action condition Attribute {attribute.name!r} "
                "supports only string, boolean, integer, or float values"
            )
        if kind == "double_value" and not isfinite(operand.double_value):
            raise ValueError(
                f"RPC {rpc_name} Action condition Attribute {attribute.name!r} "
                "float value must be finite"
            )
        operands.append(operand)
    return _RegisteredAction(
        rpc_name, action.label, permission, attribute.name, tuple(operands)
    )


def _action_permission_mappings_for_writes(
    actions: Sequence[_RegisteredAction],
    writes: Iterable[pb.AttributeWrite],
) -> pb.ActionPermissionMappings | None:
    """Return every conditioned Action's mapping when a write touches a condition source."""
    source_keys = {action.attribute_name for action in actions if action.attribute_name}
    if not source_keys or not any(write.key in source_keys for write in writes):
        return None
    return pb.ActionPermissionMappings(
        mappings=[
            pb.ActionPermissionMapping(
                attribute_key=action.attribute_name,
                equal_values=action.operands,
                required_permission=action.required_permission,
            )
            for action in actions
            if action.attribute_name
        ]
    )


def _initial_work_queue_permissions_write(
    actions: Sequence[_RegisteredAction],
    initial_writes: Iterable[pb.AttributeWrite],
) -> pb.AttributeWrite | None:
    """Return the start-time ``DexWorkQueuePermissions`` write, or ``None`` when nothing matches."""
    values = {write.key: write.value for write in initial_writes}
    permissions = sorted(
        {
            action.required_permission
            for action in actions
            if action.attribute_name and action.matches(values.get(action.attribute_name))
        }
    )
    if not permissions:
        return None
    return pb.AttributeWrite(
        key=WORK_QUEUE_PERMISSIONS_INDEX_KEY,
        value=pb.Value(
            obj_value=pb.EncodedObject(
                encoding="json",
                payload=json.dumps(permissions, separators=(",", ":")).encode(),
            )
        ),
        index_config=pb.IndexConfig(
            enable=True,
            type=pb.INDEX_TYPE_KEYWORD_ARRAY,
            index_key=WORK_QUEUE_PERMISSIONS_INDEX_KEY,
        ),
    )


def _equal_action_scalar_values(left: pb.Value, right: pb.Value) -> bool:
    kind = left.WhichOneof("kind")
    if kind not in _ACTION_SCALAR_KINDS or kind != right.WhichOneof("kind"):
        return False
    return bool(getattr(left, kind) == getattr(right, kind))
