# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta
from enum import Enum
from typing import Any, Callable, Mapping, TypeVar, overload

from dex.contracts._common import PhaseNotImplementedError, require_name
from dex.contracts.blob_cache import BlobCache
from dex.contracts.codec import Value
from dex.contracts.flow import Flow, InitialAttribute, Registry, RPCResult
from dex.contracts.state import Attribute, AttributeMap, Channel, ChannelMap, Context
from dex.contracts.step import RetryPolicy, StepDurability

InputT = TypeVar("InputT")
OutputT = TypeVar("OutputT")
ValueT = TypeVar("ValueT")


@dataclass(frozen=True)
class WorkerTarget:
    address: str
    headless: bool = False


class ActiveStepSearchMode(Enum):
    DEFAULT = "default"
    ALL = "all"


class IdReusePolicy(Enum):
    DEFAULT = "default"
    ALLOW_IF_NOT_RUNNING = "allow_if_not_running"
    ALLOW_TERMINATE_IF_RUNNING = "allow_terminate_if_running"
    DISALLOW = "disallow"


class FlowStatus(Enum):
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    CANCELLED = "cancelled"
    TERMINATED = "terminated"
    TIMED_OUT = "timed_out"


@dataclass(frozen=True)
class FlowConfig:
    active_step_search_mode: ActiveStepSearchMode | None = None
    continue_as_new_threshold: int | None = None
    continue_as_new_page_size_bytes: int | None = None
    step_durability: StepDurability | None = None
    worker_target: WorkerTarget | None = None


@dataclass(frozen=True)
class StartFlowOptions:
    timeout: timedelta | None = None
    start_delay: timedelta | None = None
    id_reuse_policy: IdReusePolicy = IdReusePolicy.DEFAULT
    cron_schedule: str | None = None
    retry_policy: RetryPolicy | None = None
    attributes: tuple[InitialAttribute[Any], ...] = ()
    config_override: FlowConfig | None = None
    ignore_already_started: bool = False
    request_id: str | None = None


@dataclass(frozen=True)
class FlowInfo:
    flow_id: str
    run_id: str
    flow_type: str
    status: FlowStatus
    started_at: datetime


@dataclass(frozen=True)
class StepExecutionId:
    step_type: str
    number: int = 0


@dataclass(frozen=True)
class TimerId:
    condition_id: str | None = None
    condition_index: int | None = None

    @staticmethod
    def by_condition_id(condition_id: str) -> TimerId:
        require_name(condition_id)
        return TimerId(condition_id=condition_id)

    @staticmethod
    def by_condition_index(condition_index: int) -> TimerId:
        return TimerId(condition_index=condition_index)


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


@dataclass(frozen=True)
class ClientOptions:
    server_address: str = "localhost:8801"
    worker_target: WorkerTarget | None = None


@dataclass(frozen=True)
class WorkerOptions:
    bind_address: str = ":8803"
    worker_target: WorkerTarget | None = None
    server_address: str = "localhost:8801"


class Client:
    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: ClientOptions = ClientOptions(),
    ) -> None:
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options

    def start_flow(
        self,
        flow: Flow[InputT],
        flow_id: str,
        input: InputT,
        options: StartFlowOptions = StartFlowOptions(),
    ) -> str:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context, InputT], RPCResult[OutputT]],
        flow_id: str,
        input: InputT,
        *,
        run_id: str = "",
    ) -> OutputT: ...

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context], RPCResult[OutputT]],
        flow_id: str,
        *,
        run_id: str = "",
    ) -> OutputT: ...

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context, InputT], None],
        flow_id: str,
        input: InputT,
        *,
        run_id: str = "",
    ) -> None: ...

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context], None],
        flow_id: str,
        *,
        run_id: str = "",
    ) -> None: ...

    def invoke_rpc(
        self,
        rpc_method: Callable[..., Any],
        flow_id: str,
        input: object = None,
        *,
        run_id: str = "",
    ) -> Any:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    @overload
    def get_attribute(
        self,
        flow_id: str,
        attribute: Attribute[ValueT],
        *,
        run_id: str = "",
    ) -> ValueT: ...

    @overload
    def get_attribute(
        self,
        flow_id: str,
        attribute: AttributeMap[ValueT],
        instance: str,
        *,
        run_id: str = "",
    ) -> ValueT: ...

    def get_attribute(
        self,
        flow_id: str,
        attribute: Attribute[Any] | AttributeMap[Any],
        instance: str | None = None,
        *,
        run_id: str = "",
    ) -> Any:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    @overload
    def set_attribute(
        self,
        flow_id: str,
        attribute: Attribute[ValueT],
        value: ValueT,
        *,
        run_id: str = "",
    ) -> None: ...

    @overload
    def set_attribute(
        self,
        flow_id: str,
        attribute: AttributeMap[ValueT],
        instance: str,
        value: ValueT,
        *,
        run_id: str = "",
    ) -> None: ...

    def set_attribute(
        self,
        flow_id: str,
        attribute: Attribute[Any] | AttributeMap[Any],
        *args: object,
        run_id: str = "",
        **kwargs: object,
    ) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    @overload
    def publish(
        self,
        flow_id: str,
        channel: Channel[ValueT],
        *values: ValueT,
        run_id: str = "",
    ) -> None: ...

    @overload
    def publish(
        self,
        flow_id: str,
        channel: ChannelMap[ValueT],
        instance: str,
        *values: ValueT,
        run_id: str = "",
    ) -> None: ...

    def publish(
        self,
        flow_id: str,
        channel: Channel[Any] | ChannelMap[Any],
        *args: object,
        run_id: str = "",
        **kwargs: object,
    ) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    @overload
    def wait_for_flow(self, flow_id: str) -> None: ...

    @overload
    def wait_for_flow(
        self,
        flow_id: str,
        output_type: type[OutputT],
        timeout: timedelta | None = None,
    ) -> OutputT: ...

    def wait_for_flow(
        self,
        flow_id: str,
        output_type: type[Any] | None = None,
        timeout: timedelta | None = None,
    ) -> Any:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def stop_flow(
        self,
        flow_id: str,
        options: StopFlowOptions = StopFlowOptions(),
    ) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def describe_flow(self, flow_id: str) -> FlowInfo:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def reset_flow(self, flow_id: str, options: ResetFlowOptions) -> str:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def skip_timer(
        self,
        flow_id: str,
        step_execution_id: StepExecutionId,
        timer_id: TimerId,
    ) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def wait_for_step_completion(
        self,
        flow_id: str,
        step_execution_id: StepExecutionId,
        timeout: timedelta,
    ) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def update_flow_config(self, flow_id: str, config: FlowConfig) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def close(self) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")


class Worker:
    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: WorkerOptions = WorkerOptions(),
    ) -> None:
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options

    def start(self) -> None:
        raise PhaseNotImplementedError("Worker runtime belongs to a later phase")

    def stop(self) -> None:
        raise PhaseNotImplementedError("Worker runtime belongs to a later phase")


@dataclass(frozen=True)
class HealthInfo:
    condition: str
    hostname: str
    duration_seconds: int


@dataclass(frozen=True)
class SearchFlowEntry:
    flow_id: str
    run_id: str
    flow_type: str
    status: str
    started_at: datetime
    closed_at: datetime | None
    attributes: Mapping[str, Value]
