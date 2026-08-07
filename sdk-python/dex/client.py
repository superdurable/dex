# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from datetime import timedelta, timezone
from types import TracebackType
from typing import Any, Callable, TypeVar, cast, overload
from uuid import uuid4

import grpc

from dex._grpc_errors import translate_rpc_error
from dex._utils import require_name
from dex._value_hydrator import ValueHydrator
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.attribute import Attribute, AttributeMap
from dex.blob_cache import BlobCache
from dex.channel import Channel, ChannelMap
from dex.client_options import ClientOptions
from dex.context import Context
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc
from dex.flow import Flow, Registry, RPCResult
from dex.flow_config import ActiveStepSearchMode, FlowConfig
from dex.flow_info import FlowInfo, FlowStatus, SearchFlowEntry, SearchFlowsPage
from dex.flow_options import (
    IdReusePolicy,
    ResetFlowOptions,
    ResetType,
    StartFlowOptions,
    StopFlowOptions,
    StopType,
)
from dex.runtime_errors import (
    FlowErrorType,
    FlowUncompletedError,
    LongPollTimeoutError,
)
from dex.step import RetryPolicy, StepDurability
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
        self._channel = grpc.insecure_channel(self.options.server_address)
        self._service = dex_pb2_grpc.FlowServiceStub(  # type: ignore[no-untyped-call]
            self._channel
        )
        self._values = ValueMapper(registry.codec_registry)
        self._hydrator = ValueHydrator(self._service, blob_cache)
        self._mappings = WorkerDispatcher(
            registry,
            self._values,
            self._hydrator,
        )
        self._closed = False

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
        registered = self.registry._flow_for_instance(flow)
        request = pb.StartFlowRequest(
            flow_id=require_name(flow_id),
            flow_type=registered.name,
            request_id=options.request_id or str(uuid4()),
            flow_start_options=self._map_start_options(options),
        )
        if registered.start_step is not None:
            start = registered.start_step
            request.start_step_type = start.name
            request.step_input.CopyFrom(self._values.encode(input, start.input_codec))
            step_options = self._mappings.map_step_options(
                registered,
                start.step.get_step_options(),
            )
            if step_options is not None:
                request.step_options.CopyFrom(step_options)
            request.step_options.skip_wait_for = start.skips_wait_for
        elif input is not None:
            raise ValueError("Flow without a start Step requires None input")
        if options.timeout is not None:
            request.flow_timeout_seconds = self._seconds32(options.timeout)
        response = cast(
            pb.StartFlowResponse, self._call(self._service.StartFlow, request)
        )
        return response.run_id

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
        _, rpc = self.registry._rpc_for_method(rpc_method)
        encoded_input = (
            self._values.encode(input, rpc.input_codec)
            if rpc.input_codec is not None
            else self._values.encode_dynamic(None)
        )
        timeout = (
            self._seconds32(rpc.options.timeout)
            if rpc.options.timeout is not None
            else 0
        )
        response = cast(
            pb.InvokeRPCResponse,
            self._call(
                self._service.InvokeRPC,
                pb.InvokeRPCRequest(
                    flow_id=require_name(flow_id),
                    run_id=run_id,
                    rpc_name=rpc.name,
                    input=encoded_input,
                    timeout_seconds=timeout,
                    lock_attribute_keys=rpc.locks,
                    request_id=str(uuid4()),
                ),
            ),
        )
        if rpc.output_codec is None:
            return None
        return self._values.decode(
            self._hydrator.hydrate(response.output),
            rpc.output_codec,
        )

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
        key = self._definition_name(attribute, instance)
        response = cast(
            pb.GetAttributesResponse,
            self._call(
                self._service.GetAttributes,
                pb.GetAttributesRequest(
                    flow_id=require_name(flow_id),
                    run_id=run_id,
                    keys=[key],
                ),
            ),
        )
        if not response.attributes:
            return None
        value = self._hydrator.hydrate(response.attributes[0].value)
        return self._values.decode(value, self._values.codec(attribute.value_type))

    @overload
    def set_attribute(
        self,
        flow_id: str,
        attribute: Attribute[ValueT],
        value: ValueT,
        /,
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
        /,
        *,
        run_id: str = "",
    ) -> None: ...

    def set_attribute(
        self,
        flow_id: str,
        attribute: Attribute[Any] | AttributeMap[Any],
        /,
        *args: object,
        run_id: str = "",
    ) -> None:
        instance, value = self._definition_value(attribute, args)
        write = pb.AttributeWrite(
            key=self._definition_name(attribute, instance),
            value=self._values.encode(
                value,
                self._values.codec(attribute.value_type),
            ),
        )
        index = self._values.index_config(
            attribute.index,
            isinstance(attribute, AttributeMap),
        )
        if index is not None:
            write.index_config.CopyFrom(index)
        self._call(
            self._service.SetAttributes,
            pb.SetAttributesRequest(
                flow_id=require_name(flow_id),
                run_id=run_id,
                attributes=[write],
                request_id=str(uuid4()),
            ),
        )

    @overload
    def publish(
        self,
        flow_id: str,
        channel: Channel[ValueT],
        /,
        *values: ValueT,
        run_id: str = "",
    ) -> None: ...

    @overload
    def publish(
        self,
        flow_id: str,
        channel: ChannelMap[ValueT],
        instance: str,
        /,
        *values: ValueT,
        run_id: str = "",
    ) -> None: ...

    def publish(
        self,
        flow_id: str,
        channel: Channel[Any] | ChannelMap[Any],
        /,
        *args: object,
        run_id: str = "",
    ) -> None:
        if isinstance(channel, ChannelMap):
            if len(args) < 2 or not isinstance(args[0], str):
                raise TypeError("ChannelMap publish requires instance and values")
            instance = args[0]
            values = args[1:]
        else:
            instance = None
            values = args
        if not values:
            raise ValueError("publish requires at least one value")
        name = self._definition_name(channel, instance)
        codec = self._values.codec(channel.value_type)
        self._call(
            self._service.PublishToChannel,
            pb.PublishToChannelRequest(
                flow_id=require_name(flow_id),
                run_id=run_id,
                messages=[
                    pb.ChannelMessage(
                        channel_name=name,
                        value=self._values.encode(value, codec),
                    )
                    for value in values
                ],
            ),
        )

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
        response = self._wait_for_flow_response(flow_id, timeout)
        if output_type is None:
            return None
        codec = self._values.codec(output_type)
        for result in reversed(response.results):
            if result.HasField("completed_step_output"):
                return self._values.decode(
                    self._hydrator.hydrate(result.completed_step_output),
                    codec,
                )
        return None

    def stop_flow(
        self,
        flow_id: str,
        options: StopFlowOptions = StopFlowOptions(),
    ) -> None:
        self._call(
            self._service.StopFlow,
            pb.StopFlowRequest(
                flow_id=require_name(flow_id),
                reason=options.reason or "",
                stop_type={
                    StopType.CANCEL: pb.STOP_TYPE_CANCEL,
                    StopType.TERMINATE: pb.STOP_TYPE_TERMINATE,
                    StopType.FAIL: pb.STOP_TYPE_FAIL,
                }[options.type],
            ),
        )

    def describe_flow(self, flow_id: str) -> FlowInfo:
        response = cast(
            pb.GetFlowSummaryResponse,
            self._call(
                self._service.GetFlowSummary,
                pb.GetFlowSummaryRequest(flow_id=require_name(flow_id)),
            ),
        )
        return FlowInfo(
            response.flow_execution_id.flow_id,
            response.flow_execution_id.run_id,
            response.flow_type,
            self._map_flow_status(response.flow_status),
            response.start_time.ToDatetime(tzinfo=timezone.utc),
        )

    def search_flows(
        self,
        query: str,
        page_size: int,
        next_page_token: str = "",
    ) -> SearchFlowsPage:
        if page_size < 0:
            raise ValueError("search page size must not be negative")
        response = cast(
            pb.SearchFlowsResponse,
            self._call(
                self._service.SearchFlows,
                pb.SearchFlowsRequest(
                    query=query,
                    page_size=page_size,
                    next_page_token=next_page_token,
                ),
            ),
        )
        flows = [self._map_search_entry(entry) for entry in response.flow_runs]
        return SearchFlowsPage(flows, response.next_page_token)

    def _map_search_entry(self, entry: pb.SearchFlowsResponseEntry) -> SearchFlowEntry:
        attributes = {
            kv.key: self._values.to_value(self._hydrator.hydrate(kv.value))
            for kv in entry.search_attributes
        }
        closed_at = (
            entry.close_time.ToDatetime(tzinfo=timezone.utc)
            if entry.HasField("close_time")
            else None
        )
        return SearchFlowEntry(
            entry.flow_id,
            entry.run_id,
            entry.flow_type,
            self._map_flow_status(entry.flow_status),
            entry.start_time.ToDatetime(tzinfo=timezone.utc),
            closed_at,
            attributes,
        )

    def reset_flow(self, flow_id: str, options: ResetFlowOptions) -> str:
        request = pb.ResetFlowRequest(
            flow_id=require_name(flow_id),
            reset_type={
                ResetType.BEGINNING: pb.FLOW_RESET_TYPE_BEGINNING,
                ResetType.HISTORY_EVENT_ID: pb.FLOW_RESET_TYPE_HISTORY_EVENT_ID,
                ResetType.HISTORY_EVENT_TIME: pb.FLOW_RESET_TYPE_HISTORY_EVENT_TIME,
                ResetType.STEP_TYPE: pb.FLOW_RESET_TYPE_STEP_TYPE,
                ResetType.STEP_EXECUTION_ID: pb.FLOW_RESET_TYPE_STEP_EXECUTION_ID,
            }[options.type],
            reason=options.reason or "",
            skip_channel_messages_reapply=options.skip_channel_messages_reapply,
            skip_locking_rpc_reapply=options.skip_locking_rpc_reapply,
        )
        if options.history_event_id is not None:
            request.history_event_id = options.history_event_id
        if options.history_event_time is not None:
            request.history_event_time = options.history_event_time.isoformat()
        if options.step_type is not None:
            request.step_type = options.step_type
        if options.step_execution_id is not None:
            request.step_execution_id = options.step_execution_id
        response = cast(
            pb.ResetFlowResponse,
            self._call(self._service.ResetFlow, request),
        )
        return response.run_id

    def skip_timer(
        self,
        flow_id: str,
        step_execution_id: StepExecutionId,
        timer_id: TimerId,
    ) -> None:
        request = pb.SkipTimerRequest(
            flow_id=require_name(flow_id),
            step_execution_id=(
                f"{step_execution_id.step_type}-{step_execution_id.number}"
            ),
        )
        if timer_id.condition_id is not None:
            request.timer_condition_id = timer_id.condition_id
        if timer_id.condition_index is not None:
            request.timer_condition_index = timer_id.condition_index
        self._call(self._service.SkipTimer, request)

    def wait_for_step_completion(
        self,
        flow_id: str,
        step_execution_id: StepExecutionId,
        timeout: timedelta,
    ) -> None:
        self._call(
            self._service.WaitForStepCompletion,
            pb.WaitForStepCompletionRequest(
                flow_id=require_name(flow_id),
                step_type=step_execution_id.step_type,
                step_execution_number=str(step_execution_id.number),
                wait_time_seconds=self._seconds32(timeout),
                request_id=str(uuid4()),
            ),
        )

    def update_flow_config(self, flow_id: str, config: FlowConfig) -> None:
        self._call(
            self._service.UpdateFlowConfig,
            pb.UpdateFlowConfigRequest(
                flow_id=require_name(flow_id),
                flow_config=self._map_flow_config(config),
            ),
        )

    def trigger_continue_as_new(self, flow_id: str) -> None:
        self._call(
            self._service.TriggerContinueAsNew,
            pb.TriggerContinueAsNewRequest(flow_id=require_name(flow_id)),
        )

    def health_check(self) -> bool:
        from google.protobuf import empty_pb2

        self._call(self._service.HealthCheck, empty_pb2.Empty())
        return True

    def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        self._channel.close()

    def _wait_for_flow_response(
        self,
        flow_id: str,
        timeout: timedelta | None,
    ) -> pb.WaitForFlowResponse:
        request = pb.WaitForFlowRequest(
            flow_id=require_name(flow_id),
            needs_results=True,
        )
        if timeout is not None:
            request.wait_time_seconds = self._seconds32(timeout)
        try:
            response = cast(pb.WaitForFlowResponse, self._service.WaitForFlow(request))
        except grpc.RpcError as error:
            if error.code() is grpc.StatusCode.DEADLINE_EXCEEDED:
                raise LongPollTimeoutError(flow_id) from error
            raise translate_rpc_error(error) from error
        if response.flow_status != pb.FLOW_STATUS_COMPLETED:
            info = self.describe_flow(flow_id)
            results = self._hydrator.step_outputs(list(response.results))
            raise FlowUncompletedError(
                info.run_id,
                self._map_flow_status(response.flow_status),
                self._map_flow_error_type(response.error_type),
                response.error_message or None,
                results,
                self._values,
            )
        return response

    def _map_start_options(self, options: StartFlowOptions) -> pb.FlowStartOptions:
        mapped = pb.FlowStartOptions(
            id_reuse_policy={
                IdReusePolicy.DEFAULT: pb.ID_REUSE_POLICY_UNSPECIFIED,
                IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED: (
                    pb.ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY
                ),
                IdReusePolicy.ALLOW_IF_NOT_RUNNING: (
                    pb.ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING
                ),
                IdReusePolicy.ALLOW_TERMINATE_IF_RUNNING: (
                    pb.ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING
                ),
                IdReusePolicy.DISALLOW: pb.ID_REUSE_POLICY_DISALLOW_REUSE,
            }[options.id_reuse_policy],
            cron_schedule=options.cron_schedule or "",
            flow_already_started_options=pb.FlowAlreadyStartedOptions(
                ignore_already_started_error=options.ignore_already_started
            ),
        )
        if options.start_delay is not None:
            mapped.flow_start_delay_seconds = self._seconds32(options.start_delay)
        if options.retry_policy is not None:
            mapped.retry_policy.CopyFrom(self._map_flow_retry(options.retry_policy))
        for initialization in options._attribute_initializations:
            definition = initialization.definition
            key = self._definition_name(definition, initialization.instance)
            mapped.attributes.append(
                pb.AttributeWrite(
                    key=key,
                    value=self._values.encode(
                        initialization.value,
                        self._values.codec(definition.value_type),
                    ),
                )
            )
        if (
            options.config_override is not None
            or self.options.worker_target is not None
        ):
            mapped.flow_config_override.CopyFrom(
                self._map_flow_config(options.config_override)
            )
        return mapped

    def _map_flow_config(self, config: FlowConfig | None) -> pb.FlowConfig:
        mapped = pb.FlowConfig()
        if config is not None:
            if config.active_step_search_mode is not None:
                mapped.active_step_search_mode = {
                    ActiveStepSearchMode.DEFAULT: (
                        pb.ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED
                    ),
                    ActiveStepSearchMode.ALL: (
                        pb.ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL
                    ),
                    ActiveStepSearchMode.WITH_WAIT_FOR: (
                        pb.ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR
                    ),
                    ActiveStepSearchMode.DISABLED: (
                        pb.ACTIVE_STEP_SEARCH_MODE_DISABLED
                    ),
                }[config.active_step_search_mode]
            if config.continue_as_new_threshold is not None:
                mapped.continue_as_new_threshold = config.continue_as_new_threshold
            if config.continue_as_new_page_size_bytes is not None:
                mapped.continue_as_new_page_size_in_bytes = (
                    config.continue_as_new_page_size_bytes
                )
            if config.step_durability is not None:
                mapped.step_durability = {
                    StepDurability.DEFAULT: pb.STEP_DURABILITY_UNSPECIFIED,
                    StepDurability.SYNC: pb.STEP_DURABILITY_SYNC,
                    StepDurability.ASYNC: pb.STEP_DURABILITY_ASYNC,
                }[config.step_durability]
        target = (
            config.worker_target
            if config is not None and config.worker_target is not None
            else self.options.worker_target
        )
        if target is not None:
            mapped.worker_target.CopyFrom(
                pb.WorkerTarget(
                    address=target.address,
                    is_headless_address=target.headless,
                )
            )
        return mapped

    @staticmethod
    def _map_flow_retry(retry: RetryPolicy) -> pb.FlowRetryPolicy:
        mapped = pb.FlowRetryPolicy(
            backoff_coefficient=retry.backoff_coefficient,
            maximum_attempts=retry.maximum_attempts,
        )
        if retry.initial_interval is not None:
            mapped.initial_interval_seconds = Client._seconds32(retry.initial_interval)
        if retry.maximum_interval is not None:
            mapped.maximum_interval_seconds = Client._seconds32(retry.maximum_interval)
        return mapped

    @staticmethod
    def _definition_name(
        definition: Attribute[Any] | AttributeMap[Any] | Channel[Any] | ChannelMap[Any],
        instance: str | None,
    ) -> str:
        if isinstance(definition, (AttributeMap, ChannelMap)):
            if instance is None:
                raise ValueError("dynamic definition requires an instance")
            return Registry.physical_name(definition.name, instance)
        if instance is not None:
            raise ValueError("static definition cannot use an instance")
        return definition.name

    @staticmethod
    def _definition_value(
        definition: Attribute[Any] | AttributeMap[Any],
        args: tuple[object, ...],
    ) -> tuple[str | None, object]:
        if isinstance(definition, Attribute) and len(args) == 1:
            return None, args[0]
        if (
            isinstance(definition, AttributeMap)
            and len(args) == 2
            and isinstance(args[0], str)
        ):
            return args[0], args[1]
        raise TypeError("set_attribute received invalid arguments")

    @staticmethod
    def _seconds32(duration: timedelta) -> int:
        seconds = duration.total_seconds()
        if seconds < 0 or not seconds.is_integer() or seconds > 2**31 - 1:
            raise ValueError("duration must be whole seconds within int32")
        return int(seconds)

    @staticmethod
    def _map_flow_status(status: int) -> FlowStatus:
        statuses: dict[int, FlowStatus] = {
            int(pb.FLOW_STATUS_RUNNING): FlowStatus.RUNNING,
            int(pb.FLOW_STATUS_COMPLETED): FlowStatus.COMPLETED,
            int(pb.FLOW_STATUS_FAILED): FlowStatus.FAILED,
            int(pb.FLOW_STATUS_TIMEOUT): FlowStatus.TIMED_OUT,
            int(pb.FLOW_STATUS_TERMINATED): FlowStatus.TERMINATED,
            int(pb.FLOW_STATUS_CANCELED): FlowStatus.CANCELED,
            int(pb.FLOW_STATUS_CONTINUED_AS_NEW): FlowStatus.CONTINUED_AS_NEW,
        }
        try:
            return statuses[status]
        except KeyError as error:
            raise ValueError(f"unknown Flow status {status}") from error

    @staticmethod
    def _map_flow_error_type(error_type: int) -> FlowErrorType | None:
        error_types: dict[int, FlowErrorType] = {
            int(pb.FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW): (
                FlowErrorType.STEP_DECISION_FAILED
            ),
            int(
                pb.FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW
            ): FlowErrorType.CLIENT_API_FAILED,
            int(pb.FLOW_ERROR_TYPE_WORKER_API_FAIL): FlowErrorType.WORKER_API_FAILED,
            int(pb.FLOW_ERROR_TYPE_INVALID_USER_FLOW_CODE): (
                FlowErrorType.INVALID_USER_FLOW_CODE
            ),
            int(pb.FLOW_ERROR_TYPE_INTERNAL): FlowErrorType.INTERNAL,
        }
        return error_types.get(error_type)

    @staticmethod
    def _call(method: Callable[[Any], Any], request: Any) -> Any:
        try:
            return method(request)
        except grpc.RpcError as error:
            raise translate_rpc_error(error) from error
