# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from dataclasses import dataclass, replace
from datetime import datetime, timedelta
from enum import Enum
from typing import Any, TypeVar, overload

from dex._utils import require_name
from dex.attribute import Attribute, AttributeMap
from dex.flow_config import FlowConfig
from dex.step import RetryPolicy

ValueT = TypeVar("ValueT")


class IdReusePolicy(Enum):
    """Control whether ``start_flow`` may reuse an existing Flow ID.

    Attributes:
        DEFAULT: Use the Dex server's default reuse policy.
        ALLOW_IF_PREVIOUS_FAILED: Reuse only after an unsuccessful closed run.
        ALLOW_IF_NOT_RUNNING: Reuse after any closed run.
        ALLOW_TERMINATE_IF_RUNNING: Terminate an active run and start the new run.
        DISALLOW: Reject every previously used Flow ID.
    """

    DEFAULT = "default"
    ALLOW_IF_PREVIOUS_FAILED = "allow_if_previous_failed"
    ALLOW_IF_NOT_RUNNING = "allow_if_not_running"
    ALLOW_TERMINATE_IF_RUNNING = "allow_terminate_if_running"
    DISALLOW = "disallow"


@dataclass(frozen=True)
class _AttributeInitialization:
    definition: Attribute[Any] | AttributeMap[Any]
    instance: str | None
    value: object


@dataclass(frozen=True)
class StartFlowOptions:
    """Configure creation of a new Flow execution.

    Durations are :class:`datetime.timedelta` values. ``None`` uses the registered
    Flow or server default. Use :meth:`with_attribute` to add initial Attribute writes.

    Attributes:
        timeout: Optional maximum lifetime of the Flow.
        start_delay: Optional delay before the starting Step becomes eligible.
        id_reuse_policy: Flow ID reuse policy; defaults to ``DEFAULT``.
        cron_schedule: Optional server-supported cron expression for recurring runs.
        retry_policy: Optional Flow-level retry policy.
        config_override: Optional FlowConfig applied over registered defaults.
        ignore_already_started: Return the existing run rather than raising
            ``FlowAlreadyStartedError`` when supported by the server.
        request_id: Optional non-empty idempotency key. ``None`` generates one.

    Examples:
        >>> options = (
        ...     StartFlowOptions(timeout=timedelta(hours=1))
        ...     .with_attribute(status, "pending")
        ...     .with_attribute(balances, "merchant-7", 1200)
        ... )
    """

    timeout: timedelta | None = None
    start_delay: timedelta | None = None
    id_reuse_policy: IdReusePolicy = IdReusePolicy.DEFAULT
    cron_schedule: str | None = None
    retry_policy: RetryPolicy | None = None
    _attribute_initializations: tuple[_AttributeInitialization, ...] = ()
    config_override: FlowConfig | None = None
    ignore_already_started: bool = False
    request_id: str | None = None

    @overload
    def with_attribute(
        self,
        attribute: Attribute[ValueT],
        value: ValueT,
        /,
    ) -> StartFlowOptions: ...

    @overload
    def with_attribute(
        self,
        attribute: AttributeMap[ValueT],
        instance: str,
        value: ValueT,
        /,
    ) -> StartFlowOptions: ...

    def with_attribute(
        self,
        attribute: Attribute[Any] | AttributeMap[Any],
        /,
        *args: object,
    ) -> StartFlowOptions:
        """Return a copy with one initial Attribute value appended.

        Singleton Attributes take ``(attribute, value)``; AttributeMaps take
        ``(attribute, instance, value)``. Calls preserve initialization order.

        Args:
            attribute: A registered Attribute or AttributeMap definition.
            *args: The value, or the AttributeMap instance followed by its value.

        Returns:
            A new options object containing the additional initialization.

        Raises:
            TypeError: If arguments do not match either supported form.
            ValueError: If an AttributeMap instance is empty.
        """
        if isinstance(attribute, Attribute) and len(args) == 1:
            initialization = _AttributeInitialization(attribute, None, args[0])
        elif isinstance(attribute, AttributeMap) and len(args) == 2:
            instance = args[0]
            if not isinstance(instance, str):
                raise TypeError("attribute-map instance must be a string")
            require_name(instance)
            initialization = _AttributeInitialization(attribute, instance, args[1])
        else:
            raise TypeError("with_attribute received invalid arguments")
        return replace(
            self,
            _attribute_initializations=self._attribute_initializations
            + (initialization,),
        )


class ResetType(Enum):
    """Select the history point from which a Flow reset should resume.

    Attributes:
        BEGINNING: Restart from the beginning of the Flow.
        HISTORY_EVENT_ID: Resume at a specific history event ID.
        HISTORY_EVENT_TIME: Resume at the first event at or after a timestamp.
        STEP_TYPE: Resume at the latest execution of a Step type.
        STEP_EXECUTION_ID: Resume at one exact Step execution ID.
    """

    BEGINNING = "beginning"
    HISTORY_EVENT_ID = "history_event_id"
    HISTORY_EVENT_TIME = "history_event_time"
    STEP_TYPE = "step_type"
    STEP_EXECUTION_ID = "step_execution_id"


@dataclass(frozen=True)
class ResetFlowOptions:
    """Configure creation of a new run from existing Flow history.

    Exactly one selector matching ``type`` must be set. The Client
    validates combinations before sending the request.

    Attributes:
        type: The reset-point selector kind.
        history_event_id: Event ID used by ``HISTORY_EVENT_ID``.
        history_event_time: Timestamp used by ``HISTORY_EVENT_TIME``.
        step_type: Registered Step type used by ``STEP_TYPE``.
        step_execution_id: Exact execution ID used by ``STEP_EXECUTION_ID``.
        reason: Optional operator-readable reset reason.
        skip_writes_reapply: Do not replay RPCs, Channel publications, or Attribute
            writes after the reset point.
    """

    type: ResetType
    history_event_id: int | None = None
    history_event_time: datetime | None = None
    step_type: str | None = None
    step_execution_id: str | None = None
    reason: str | None = None
    skip_writes_reapply: bool = False


class StopType(Enum):
    """Select how an active Flow should close.

    Attributes:
        CANCEL: Request cooperative cancellation.
        TERMINATE: Force immediate termination.
        FAIL: Close the Flow as failed with the reason in :class:`StopFlowOptions`.
    """

    CANCEL = "cancel"
    TERMINATE = "terminate"
    FAIL = "fail"


@dataclass(frozen=True)
class StopFlowOptions:
    """Configure a ``stop_flow`` request.

    Attributes:
        type: The stop behavior; defaults to cooperative cancellation.
        reason: Optional operator-readable reason recorded by Dex.
    """

    type: StopType = StopType.CANCEL
    reason: str | None = None
