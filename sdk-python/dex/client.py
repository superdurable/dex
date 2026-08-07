# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from datetime import timedelta
from types import TracebackType
from typing import Any, Callable, TypeVar, overload

from dex._utils import PhaseNotImplementedError
from dex.attribute import Attribute, AttributeMap
from dex.blob_cache import BlobCache
from dex.channel import Channel, ChannelMap
from dex.client_options import ClientOptions
from dex.context import Context
from dex.flow import Flow, Registry, RPCResult
from dex.flow_config import FlowConfig
from dex.flow_info import FlowInfo
from dex.flow_options import ResetFlowOptions, StartFlowOptions, StopFlowOptions
from dex.step import MaybeAwaitable
from dex.step_execution import StepExecutionId, TimerId

InputT = TypeVar("InputT")
OutputT = TypeVar("OutputT")
ValueT = TypeVar("ValueT")


class Client:
    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: ClientOptions | None = None,
    ) -> None:
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options or ClientOptions()

    def __enter__(self) -> Client:
        return self

    def __exit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self.close()

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
        rpc_method: Callable[[Context, InputT], MaybeAwaitable[RPCResult[OutputT]]],
        flow_id: str,
        input: InputT,
        *,
        run_id: str = "",
    ) -> OutputT: ...

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context], MaybeAwaitable[RPCResult[OutputT]]],
        flow_id: str,
        *,
        run_id: str = "",
    ) -> OutputT: ...

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context, InputT], MaybeAwaitable[None]],
        flow_id: str,
        input: InputT,
        *,
        run_id: str = "",
    ) -> None: ...

    @overload
    def invoke_rpc(
        self,
        rpc_method: Callable[[Context], MaybeAwaitable[None]],
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

    def trigger_continue_as_new(self, flow_id: str) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")

    def close(self) -> None:
        raise PhaseNotImplementedError("Client transport belongs to a later phase")
