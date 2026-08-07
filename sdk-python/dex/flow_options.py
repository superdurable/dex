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
    BEGINNING = "beginning"
    HISTORY_EVENT_ID = "history_event_id"
    HISTORY_EVENT_TIME = "history_event_time"
    STEP_TYPE = "step_type"
    STEP_EXECUTION_ID = "step_execution_id"


@dataclass(frozen=True)
class ResetFlowOptions:
    type: ResetType
    history_event_id: int | None = None
    history_event_time: datetime | None = None
    step_type: str | None = None
    step_execution_id: str | None = None
    reason: str | None = None
    skip_channel_messages_reapply: bool = False
    skip_locking_rpc_reapply: bool = False


class StopType(Enum):
    CANCEL = "cancel"
    TERMINATE = "terminate"
    FAIL = "fail"


@dataclass(frozen=True)
class StopFlowOptions:
    type: StopType = StopType.CANCEL
    reason: str | None = None
