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

from dex._async_value_hydrator import AsyncValueHydrator
from dex._grpc_errors import FlowTargetRequirement, translate_rpc_error
from dex._utils import require_name
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.attribute import Attribute, AttributeMap, _apply_attribute_store_sync
from dex.blob_cache import BlobCache
from dex.channel import Channel, ChannelMap, ChannelMessage
from dex.client_options import ClientOptions
from dex.context import Context
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc
from dex.flow import Flow, Registry, RPCResult
from dex.flow_config import ActiveStepSearchMode, FlowConfig
from dex.flow_info import FlowInfo, FlowStatus, SearchFlowEntry, SearchFlowsPage
from dex.flow_options import (
    FlowTimeoutPolicy,
    IdReusePolicy,
    StartFlowOptions,
    StopFlowOptions,
    StopType,
    TimeTravelOptions,
    TimeTravelStepMethod,
    TimeTravelType,
    _resolve_flow_timeout_policy,
)
from dex.flow_result import FlowResult, flow_result_from_proto
from dex.runtime_errors import FlowErrorType
from dex.step import RetryPolicy, StepDurability
from dex.step_execution import StepExecutionId, TimerId
from dex.stream import Stream, StreamMessage

InputT = TypeVar("InputT")
OutputT = TypeVar("OutputT")
ValueT = TypeVar("ValueT")


class AsyncClient:
    """Call Dex FlowService asynchronously through registered Flow definitions.

    Methods perform non-blocking gRPC I/O and are safe to await from concurrent
    tasks. The Client owns its asynchronous channel but not its Registry or BlobCache;
    use ``async with`` or await ``close`` during application shutdown.

    Attributes:
        registry: Flow definitions used to validate typed calls.
        blob_cache: The open cache used to hydrate large values.
        options: The effective ClientOptions.

    Examples:
        >>> async with AsyncClient(registry, cache) as client:
        ...     run_id = await client.start_flow(orders, "order-42", input)
        ...     result = (await client.wait_for_flow("order-42")).single_output(OrderResult)
    """

    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: ClientOptions | None = None,
    ) -> None:
        """Construct an AsyncClient and its lazy plaintext gRPC channel.

        Args:
            registry: The Flow Registry used for validation and codecs.
            blob_cache: An open cache used for asynchronous hydration.
            options: Service address and default Worker target; ``None`` uses defaults.
        """
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options or ClientOptions()
        self._channel = grpc.aio.insecure_channel(self.options.server_address)
        self._service = dex_pb2_grpc.FlowServiceStub(  # type: ignore[no-untyped-call]
            self._channel
        )
        self._values = ValueMapper(registry.codec_registry)
        self._hydrator = AsyncValueHydrator(self._service, blob_cache)
        self._mappings = WorkerDispatcher(
            registry,
            self._values,
            cast(Any, self._hydrator),
        )
        self._closed = False

    async def __aenter__(self) -> AsyncClient:
        """Return this Client for asynchronous context-manager use.

        Returns:
            This AsyncClient instance.
        """
        return self

    async def __aexit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        """Close the Client when leaving an asynchronous context block.

        Args:
            exception_type: The active exception type, if any.
            exception: The active exception value, if any.
            traceback: The active traceback, if any.
        """
        await self.close()

    async def start_flow(
        self,
        flow: Flow[InputT],
        flow_id: str,
        input: InputT,
        options: StartFlowOptions = StartFlowOptions(),
    ) -> str:
        """Start a Flow and return its server-assigned run ID.

        The await completes after Dex accepts the Flow, not after completion. ``flow``
        must be the exact registered instance and ``input`` must match its starting
        Step annotation.

        Args:
            flow: The registered Flow instance to start.
            flow_id: A non-empty application ID stable across runs.
            input: The starting Step input, or ``None`` without a starting Step.
            options: Timeout, reuse, retry, idempotency, and initial-state options.

        Returns:
            The server-assigned run ID.

        Raises:
            FlowAlreadyStartedError: If reuse policy rejects an existing Flow ID.
            FlowDefinitionError: If ``flow`` is not registered.
            ValueMappingError: If an input or initial Attribute cannot be encoded.
            DexServiceError: If FlowService rejects or cannot perform the request.
        """
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
        request.flow_timeout_policy = self._resolve_timeout_policy(registered, options)
        response = cast(
            pb.StartFlowResponse,
            await self._call(
                self._service.StartFlow, request, "start_flow", flow_id, "none"
            ),
        )
        return response.run_id

    @overload
    async def invoke_rpc(
        self,
        rpc_method: Callable[[Context, InputT], RPCResult[OutputT]],
        flow_id: str,
        input: InputT,
        *,
        run_id: str = "",
    ) -> OutputT: ...

    @overload
    async def invoke_rpc(
        self,
        rpc_method: Callable[[Context], RPCResult[OutputT]],
        flow_id: str,
        *,
        run_id: str = "",
    ) -> OutputT: ...

    @overload
    async def invoke_rpc(
        self,
        rpc_method: Callable[[Context, InputT], None],
        flow_id: str,
        input: InputT,
        *,
        run_id: str = "",
    ) -> None: ...

    @overload
    async def invoke_rpc(
        self,
        rpc_method: Callable[[Context], None],
        flow_id: str,
        *,
        run_id: str = "",
    ) -> None: ...

    async def invoke_rpc(
        self,
        rpc_method: Callable[..., Any],
        flow_id: str,
        input: object = None,
        *,
        run_id: str = "",
    ) -> Any:
        """Invoke a registered RPC and await its typed result.

        Pass the bound method from the registered Flow instance. RPC timeout and
        Attribute locks come from ``@rpc`` configuration.

        Args:
            rpc_method: A bound method decorated with ``@rpc``.
            flow_id: The non-empty target Flow ID.
            input: The annotated input, or ``None`` for an input-free RPC.
            run_id: Optional exact run; ``""`` targets the active run.

        Returns:
            The decoded ``RPCResult.output``, or ``None`` for a no-output RPC.

        Raises:
            FlowDefinitionError: If the method is not registered.
            RpcLockConflictError: If Attribute locks cannot be acquired.
            WorkerInvocationError: If the application handler fails.
            ValueMappingError: If input or output mapping fails.
            DexServiceError: If the Flow is inactive or the service call fails.
        """
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
            await self._call(
                self._service.InvokeRPC,
                pb.InvokeRPCRequest(
                    flow_id=require_name(flow_id),
                    run_id=run_id,
                    rpc_name=rpc.name,
                    input=encoded_input,
                    timeout_seconds=timeout,
                    lock_attribute_keys=rpc.locks,
                    request_id=str(uuid4()),
                    is_transactional=rpc.options.is_transactional,
                ),
                "invoke_rpc",
                flow_id,
                "active",
            ),
        )
        if rpc.output_codec is None:
            return None
        return self._values.decode(
            await self._hydrator.hydrate(response.output),
            rpc.output_codec,
        )

    @overload
    async def get_attribute(
        self,
        flow_id: str,
        attribute: Attribute[ValueT],
        *,
        run_id: str = "",
    ) -> ValueT: ...

    @overload
    async def get_attribute(
        self,
        flow_id: str,
        attribute: AttributeMap[ValueT],
        instance: str,
        *,
        run_id: str = "",
    ) -> ValueT: ...

    async def get_attribute(
        self,
        flow_id: str,
        attribute: Attribute[Any] | AttributeMap[Any],
        instance: str | None = None,
        *,
        run_id: str = "",
    ) -> Any:
        """Await one decoded Attribute or AttributeMap instance.

        Args:
            flow_id: The non-empty target Flow ID.
            attribute: A typed singleton Attribute or AttributeMap definition.
            instance: The map instance. Omit it for a singleton Attribute. Slash is prohibited because it is a reserved character.
            run_id: Optional exact run; ``""`` targets the current run.

        Returns:
            The decoded value, or ``None`` when unset.

        Raises:
            TypeError: If singleton/map arguments do not match the definition.
            ValueMappingError: If the stored value cannot be decoded.
            FlowNotFoundError: If ``flow_id`` does not exist.
            DexServiceError: If FlowService cannot perform the request.
        """
        key = self._definition_name(attribute, instance)
        response = cast(
            pb.GetAttributesResponse,
            await self._call(
                self._service.GetAttributes,
                pb.GetAttributesRequest(
                    flow_id=require_name(flow_id),
                    run_id=run_id,
                    keys=[key],
                ),
                "get_attribute",
                flow_id,
                "existing",
            ),
        )
        if not response.attributes:
            return None
        value = await self._hydrator.hydrate(response.attributes[0].value)
        return self._values.decode(value, self._values.codec(attribute.value_type))

    @overload
    async def set_attribute(
        self,
        flow_id: str,
        attribute: Attribute[ValueT],
        value: ValueT,
        /,
        *,
        run_id: str = "",
    ) -> None: ...

    @overload
    async def set_attribute(
        self,
        flow_id: str,
        attribute: AttributeMap[ValueT],
        instance: str,
        value: ValueT,
        /,
        *,
        run_id: str = "",
    ) -> None: ...

    async def set_attribute(
        self,
        flow_id: str,
        attribute: Attribute[Any] | AttributeMap[Any],
        /,
        *args: object,
        run_id: str = "",
    ) -> None:
        """Write one Attribute or AttributeMap instance on an active Flow.

        Singleton form is ``set_attribute(flow_id, attribute, value)``; map form is
        ``set_attribute(flow_id, attribute_map, instance, value)``.

        Args:
            flow_id: The non-empty target Flow ID.
            attribute: A typed singleton Attribute or AttributeMap definition.
            *args: The value, or a map instance followed by its value.
            run_id: Optional exact run; ``""`` targets the active run.

        Raises:
            TypeError: If arguments or value type are invalid.
            ValueMappingError: If the value cannot be encoded.
            FlowNotActiveError: If the selected Flow run is closed.
            DexServiceError: If FlowService cannot apply the write.
        """
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
        _apply_attribute_store_sync(write, attribute)
        await self._call(
            self._service.SetAttributes,
            pb.SetAttributesRequest(
                flow_id=require_name(flow_id),
                run_id=run_id,
                attributes=[write],
                request_id=str(uuid4()),
            ),
            "set_attribute",
            flow_id,
            "active",
        )

    @overload
    async def publish(
        self,
        flow_id: str,
        channel: Channel[ValueT],
        /,
        *values: ValueT,
        run_id: str = "",
    ) -> None: ...

    @overload
    async def publish(
        self,
        flow_id: str,
        channel: ChannelMap[ValueT],
        instance: str,
        /,
        *values: ValueT,
        run_id: str = "",
    ) -> None: ...

    async def publish(
        self,
        flow_id: str,
        channel: Channel[Any] | ChannelMap[Any],
        /,
        *args: object,
        run_id: str = "",
    ) -> None:
        """Append one or more typed values to a Channel.

        Singleton form supplies values directly; ChannelMap form supplies an instance
        followed by values. Dex preserves argument order within the request.

        Args:
            flow_id: The non-empty target Flow ID.
            channel: A typed singleton Channel or ChannelMap definition.
            *args: Values, or a map instance followed by one or more values.
            run_id: Optional exact run; ``""`` targets the active run.

        Raises:
            ValueError: If no value is passed or a name is empty.
            TypeError: If map arguments or value types are invalid.
            ValueMappingError: If a value cannot be encoded.
            FlowNotActiveError: If the selected Flow run is closed.
            DexServiceError: If FlowService cannot publish the batch.
        """
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
        await self._call(
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
            "publish",
            flow_id,
            "active",
        )

    async def write_stream(
        self,
        flow_id: str,
        stream: Stream[ValueT],
        source: str,
        value: ValueT,
    ) -> None:
        """Await one typed best-effort Stream append with source metadata.

        Args:
            flow_id: Logical Flow instance ID; the Flow need not exist or be active.
            stream: Exact Stream object registered in one Flow schema.
            source: Non-empty informational source. Repeated values still append.
            value: Typed message to append.

        Raises:
            ValueError: If the Flow ID or source is empty.
            FlowDefinitionError: If the Stream is not registered.
            ValueMappingError: If the message cannot be encoded.
            DexServiceError: If FlowService cannot append the message.
        """
        require_name(source)
        flow = self.registry._flow_for_stream(stream)
        await self._call(
            self._service.WriteStream,
            pb.WriteStreamRequest(
                flow_id=require_name(flow_id),
                flow_type=flow.name,
                stream_name=stream.name,
                stream_capacity_bytes=stream.stream_capacity_bytes,
                value=self._values.encode(
                    value,
                    self._values.codec(stream.value_type),
                ),
                source=source,
            ),
            "write_stream",
            flow_id,
            "none",
        )

    @overload
    async def get_channel_messages(
        self,
        flow_id: str,
        channel: Channel[ValueT],
        /,
        *,
        run_id: str = "",
    ) -> tuple[ChannelMessage[ValueT], ...]: ...

    @overload
    async def get_channel_messages(
        self,
        flow_id: str,
        channel: ChannelMap[ValueT],
        instance: str,
        /,
        *,
        run_id: str = "",
    ) -> tuple[ChannelMessage[ValueT], ...]: ...

    async def get_channel_messages(
        self,
        flow_id: str,
        channel: Channel[Any] | ChannelMap[Any],
        instance: str | None = None,
        /,
        *,
        run_id: str = "",
    ) -> tuple[ChannelMessage[Any], ...]:
        """Return every pending message for a Channel in FIFO order.

        Args:
            flow_id: The non-empty target Flow ID.
            channel: A typed singleton Channel or ChannelMap definition.
            instance: Required ChannelMap instance; omit for a singleton Channel.
            run_id: Optional exact run; ``""`` targets the current run.

        Returns:
            Immutable typed message envelopes with server-assigned IDs.

        Raises:
            ValueMappingError: If a pending value cannot be decoded.
            FlowNotFoundError: If the selected Flow run does not exist.
            DexServiceError: If FlowService cannot read the queue.
        """
        name = self._definition_name(channel, instance)
        response = cast(
            pb.GetChannelMessagesResponse,
            await self._call(
                self._service.GetChannelMessages,
                pb.GetChannelMessagesRequest(
                    flow_id=require_name(flow_id),
                    run_id=run_id,
                    channel_name=name,
                ),
                "get_channel_messages",
                flow_id,
                "existing",
            ),
        )
        codec = self._values.codec(channel.value_type)
        messages: list[ChannelMessage[Any]] = []
        for message in response.messages:
            messages.append(
                ChannelMessage(
                    message_id=message.message_id,
                    value=self._values.decode(
                        await self._hydrator.hydrate(message.value),
                        codec,
                    ),
                )
            )
        return tuple(messages)

    @overload
    async def delete_channel_message(
        self,
        flow_id: str,
        channel: Channel[Any],
        message_id: str,
        /,
        *,
        run_id: str = "",
    ) -> None: ...

    @overload
    async def delete_channel_message(
        self,
        flow_id: str,
        channel: ChannelMap[Any],
        instance: str,
        message_id: str,
        /,
        *,
        run_id: str = "",
    ) -> None: ...

    async def delete_channel_message(
        self,
        flow_id: str,
        channel: Channel[Any] | ChannelMap[Any],
        instance_or_message_id: str,
        message_id: str | None = None,
        /,
        *,
        run_id: str = "",
    ) -> None:
        """Delete one pending Channel message by its server-assigned ID.

        Args:
            flow_id: The non-empty target Flow ID.
            channel: A typed singleton Channel or ChannelMap definition.
            instance_or_message_id: Singleton message ID or ChannelMap instance.
            message_id: ChannelMap message ID; omit for a singleton Channel.
            run_id: Optional exact run; ``""`` targets the active run.

        Raises:
            ChannelMessageNotFoundError: If the message is no longer pending.
            FlowNotActiveError: If the selected Flow run is closed.
            DexServiceError: If FlowService cannot delete the message.
        """
        instance = instance_or_message_id if isinstance(channel, ChannelMap) else None
        resolved_message_id = (
            message_id if instance is not None else instance_or_message_id
        )
        if resolved_message_id is None:
            raise ValueError("ChannelMap message ID is required")
        await self._call(
            self._service.DeleteChannelMessage,
            pb.DeleteChannelMessageRequest(
                flow_id=require_name(flow_id),
                run_id=run_id,
                channel_name=self._definition_name(channel, instance),
                message_id=require_name(resolved_message_id),
                request_id=str(uuid4()),
            ),
            "delete_channel_message",
            flow_id,
            "active",
        )

    async def read_stream(
        self,
        flow_id: str,
        stream: Stream[ValueT],
        resume_token: str = "",
        timeout: timedelta | None = None,
    ) -> StreamMessage[ValueT]:
        """Await the next retained Stream message after a resume token.

        Args:
            flow_id: Logical Flow instance ID used as the Stream instance key.
            stream: Exact Stream object registered in one Flow schema.
            resume_token: Previous message token, or empty for the retained head.
            timeout: Optional non-negative server-side long-poll duration.

        Returns:
            The decoded message, next resume token, creation time, and source.

        Raises:
            LongPollTimeoutError: If no message arrives before the server wait expires.
            FlowDefinitionError: If the Stream is not registered.
            ValueMappingError: If the retained message cannot be decoded.
            DexServiceError: If FlowService cannot perform the read.
        """
        flow = self.registry._flow_for_stream(stream)
        response = cast(
            pb.ReadStreamResponse,
            await self._call(
                self._service.ReadStream,
                pb.ReadStreamRequest(
                    flow_id=require_name(flow_id),
                    flow_type=flow.name,
                    stream_name=stream.name,
                    resume_token=resume_token,
                    wait_time_seconds=(
                        0 if timeout is None else self._seconds32(timeout)
                    ),
                ),
                "read_stream",
                flow_id,
                "none",
            ),
        )
        if (
            not response.HasField("message")
            or not response.message.HasField("value")
            or not response.message.HasField("created_time")
            or not response.message.resume_token
        ):
            raise ValueError("Dex returned an incomplete Stream message")
        return StreamMessage(
            self._values.decode(
                response.message.value,
                self._values.codec(stream.value_type),
            ),
            response.message.resume_token,
            response.message.created_time.ToDatetime(tzinfo=timezone.utc),
            response.message.source,
        )

    async def wait_for_flow(
        self,
        flow_id: str,
        timeout: timedelta | None = None,
    ) -> FlowResult:
        """Await Flow closure and return its terminal result.

        ``timeout`` is a server-side long-poll duration, not a local task deadline.
        A LongPollTimeoutError is retryable by awaiting this method again. Parallel
        completion order is not deterministic; select results by Step identity.

        Args:
            flow_id: The non-empty target Flow ID.
            timeout: Optional non-negative server-side wait duration.

        Returns:
            The terminal status, failure detail, and output-bearing completions.

        Raises:
            LongPollTimeoutError: If the Flow remains open when the wait expires.
            FlowNotFoundError: If ``flow_id`` does not exist.
            ValueMappingError: If Dex returns a malformed or incompatible hydrated value.
            DexServiceError: If FlowService cannot perform the wait.
        """
        response = await self._wait_for_flow_response(flow_id, timeout)
        hydrated = await self._hydrator.step_outputs(list(response.results))
        mapped = pb.FlowResult()
        mapped.CopyFrom(response)
        del mapped.results[:]
        mapped.results.extend(hydrated)
        return flow_result_from_proto(
            mapped,
            lambda value, output_type: self._values.decode(
                value, self._values.codec(output_type)
            ),
        )

    async def stop_flow(
        self,
        flow_id: str,
        options: StopFlowOptions = StopFlowOptions(),
    ) -> None:
        """Request cancellation, termination, or failure of an active Flow.

        The await completes after Dex accepts the request, not after terminal status.

        Args:
            flow_id: The non-empty target Flow ID.
            options: Stop mode and optional recorded reason.

        Raises:
            FlowNotActiveError: If no active run exists.
            DexServiceError: If FlowService rejects or cannot perform the request.
        """
        await self._call(
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
            "stop_flow",
            flow_id,
            "active",
        )

    async def describe_flow(self, flow_id: str) -> FlowInfo:
        """Return summary metadata for the current or latest Flow run.

        Args:
            flow_id: The non-empty Flow ID to describe.

        Returns:
            Flow ID, run ID, Flow type, status, and UTC start time.

        Raises:
            FlowNotFoundError: If ``flow_id`` does not exist.
            DexServiceError: If FlowService cannot return the summary.
        """
        response = cast(
            pb.GetFlowSummaryResponse,
            await self._call(
                self._service.GetFlowSummary,
                pb.GetFlowSummaryRequest(flow_id=require_name(flow_id)),
                "describe_flow",
                flow_id,
                "existing",
            ),
        )
        return FlowInfo(
            response.flow_execution_id.flow_id,
            response.flow_execution_id.run_id,
            response.flow_type,
            self._map_flow_status(response.flow_status),
            response.start_time.ToDatetime(tzinfo=timezone.utc),
        )

    async def search_flows(
        self,
        query: str,
        page_size: int,
        next_page_token: str = "",
    ) -> SearchFlowsPage:
        """Return one page of Flow runs matching a visibility query.

        Args:
            query: A Dex visibility query; an empty string uses server defaults.
            page_size: The non-negative maximum result count requested.
            next_page_token: Opaque token from the preceding page, or ``""`` first.

        Returns:
            Server-ordered entries with hydrated indexed Attributes and a next token.

        Raises:
            ValueError: If ``page_size`` is negative.
            ValueMappingError: If an indexed Attribute cannot be hydrated.
            DexServiceError: If FlowService cannot execute the query.
        """
        if page_size < 0:
            raise ValueError("search page size must not be negative")
        response = cast(
            pb.SearchFlowsResponse,
            await self._call(
                self._service.SearchFlows,
                pb.SearchFlowsRequest(
                    query=query,
                    page_size=page_size,
                    next_page_token=next_page_token,
                ),
                "search_flows",
                None,
                "none",
            ),
        )
        flows = [await self._map_search_entry(entry) for entry in response.flow_runs]
        return SearchFlowsPage(flows, response.next_page_token)

    async def _map_search_entry(
        self, entry: pb.SearchFlowsResponseEntry
    ) -> SearchFlowEntry:
        attributes = {
            kv.key: self._values.to_value(await self._hydrator.hydrate(kv.value))
            for kv in entry.indexed_attributes
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

    async def time_travel(self, flow_id: str, options: TimeTravelOptions) -> str:
        """Create a new run from a selected point in existing Flow history.

        Args:
            flow_id: The non-empty Flow ID whose history supplies the new run.
            options: Time travel selector, reason, and replay controls.

        Returns:
            The new server-assigned run ID; the Flow ID is unchanged.

        Raises:
            FlowNotFoundError: If ``flow_id`` does not exist.
            ValueError: If time travel selector fields are invalid.
            DexServiceError: If FlowService cannot create the new run.
        """
        request = pb.ResetFlowRequest(
            flow_id=require_name(flow_id),
            reset_type={
                TimeTravelType.BEGINNING: pb.FLOW_RESET_TYPE_BEGINNING,
                TimeTravelType.HISTORY_EVENT_TIME: pb.FLOW_RESET_TYPE_HISTORY_EVENT_TIME,
                TimeTravelType.STEP_TYPE: pb.FLOW_RESET_TYPE_STEP_TYPE,
                TimeTravelType.STEP_EXECUTION_ID: pb.FLOW_RESET_TYPE_STEP_EXECUTION_ID,
            }[options.type],
            reason=options.reason or "",
            skip_writes_reapply=options.skip_writes_reapply,
        )
        if options.history_event_time is not None:
            request.history_event_time = options.history_event_time.isoformat()
        if options.step_type is not None:
            request.step_type = options.step_type
        if options.step_execution_id is not None:
            request.step_execution_id = options.step_execution_id
        if options.step_method is not None:
            request.step_method = {
                TimeTravelStepMethod.WAIT_FOR: pb.FLOW_RESET_STEP_METHOD_WAIT_FOR,
                TimeTravelStepMethod.EXECUTE: pb.FLOW_RESET_STEP_METHOD_EXECUTE,
            }[options.step_method]
        response = cast(
            pb.ResetFlowResponse,
            await self._call(
                self._service.ResetFlow,
                request,
                "time_travel",
                flow_id,
                "existing",
            ),
        )
        return response.run_id

    async def skip_timer(
        self,
        flow_id: str,
        step_execution_id: StepExecutionId,
        timer_id: TimerId,
    ) -> None:
        """Make one waiting Timer condition immediately ready.

        Args:
            flow_id: The non-empty active Flow ID.
            step_execution_id: The Step type and positive execution number.
            timer_id: Exactly one Timer condition ID or zero-based index.

        Raises:
            FlowNotActiveError: If the Flow is closed.
            ValueError: If an identifier is invalid.
            DexServiceError: If FlowService cannot skip the Timer.
        """
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
        await self._call(
            self._service.SkipTimer, request, "skip_timer", flow_id, "active"
        )

    async def wait_for_step_completion(
        self,
        flow_id: str,
        step_execution_id: StepExecutionId,
        timeout: timedelta,
    ) -> None:
        """Await one Step execution's completion or a long-poll timeout.

        Args:
            flow_id: The non-empty active Flow ID.
            step_execution_id: The Step type and positive execution number.
            timeout: The non-negative server-side wait duration.

        Raises:
            LongPollTimeoutError: If completion is not observed before ``timeout``.
            FlowNotActiveError: If the Flow closes first.
            DexServiceError: If FlowService cannot perform the wait.
        """
        await self._call(
            self._service.WaitForStepCompletion,
            pb.WaitForStepCompletionRequest(
                flow_id=require_name(flow_id),
                step_type=step_execution_id.step_type,
                step_execution_number=str(step_execution_id.number),
                wait_time_seconds=self._seconds32(timeout),
                request_id=str(uuid4()),
            ),
            "wait_for_step_completion",
            flow_id,
            "active",
        )

    @overload
    async def wait_for_attribute_equal(
        self,
        flow_id: str,
        attribute: Attribute[ValueT],
        expected: ValueT,
        timeout: timedelta,
    ) -> None:
        """Await a singleton Attribute in the current run equaling ``expected``.

        The Client generates a request ID. JSON, bytes, and null values raise
        ``ValueError`` before transport. A remote expiry raises
        ``LongPollTimeoutError``.

        Args:
            flow_id: The non-empty active Flow ID.
            attribute: The registered singleton Attribute to observe.
            expected: The string, bool, int, or float value to await.
            timeout: The non-negative server-side wait duration.

        Raises:
            ValueError: If an identifier, timeout, or expected value is invalid.
            LongPollTimeoutError: If equality is not observed before ``timeout``.
            FlowNotActiveError: If the Flow closes first.
            DexServiceError: If FlowService cannot perform the wait.
        """
        ...

    @overload
    async def wait_for_attribute_equal(
        self,
        flow_id: str,
        attribute: AttributeMap[ValueT],
        instance: str,
        expected: ValueT,
        timeout: timedelta,
    ) -> None:
        """Await one AttributeMap instance in the current run.

        Primitive-value restrictions, request-ID generation, timeout behavior, and
        service errors match :meth:`wait_for_attribute_equal`.

        Args:
            flow_id: The non-empty active Flow ID.
            attribute: The registered AttributeMap to observe.
            instance: The map instance to observe. Slash is prohibited because it is a reserved character.
            expected: The string, bool, int, or float value to await.
            timeout: The non-negative server-side wait duration.

        Raises:
            ValueError: If an identifier, timeout, or expected value is invalid.
            LongPollTimeoutError: If equality is not observed before ``timeout``.
            FlowNotActiveError: If the Flow closes first.
            DexServiceError: If FlowService cannot perform the wait.
        """
        ...

    async def wait_for_attribute_equal(
        self,
        flow_id: str,
        attribute: Attribute[Any] | AttributeMap[Any],
        *args: object,
        **kwargs: object,
    ) -> None:
        """Await a singleton Attribute or AttributeMap instance equaling a value.

        Singleton form is ``wait_for_attribute_equal(flow_id, attribute, expected,
        timeout)``; map form adds ``instance`` before ``expected``.

        Args:
            flow_id: The non-empty active Flow ID.
            attribute: The registered Attribute or AttributeMap to observe.
            *args: Positional expected value and timeout, optionally preceded by a map instance.
            **kwargs: The same arguments supplied by name.

        Raises:
            TypeError: If arguments do not match the Attribute definition.
            ValueError: If an identifier, timeout, or expected value is invalid.
            LongPollTimeoutError: If equality is not observed before the timeout.
            FlowNotActiveError: If the Flow closes first.
            DexServiceError: If FlowService cannot perform the wait.
        """
        instance, expected, timeout = self._attribute_wait_arguments(
            attribute, args, kwargs
        )
        await self._wait_for_attribute_equal(
            flow_id, attribute, instance, expected, timeout
        )

    async def _wait_for_attribute_equal(
        self,
        flow_id: str,
        attribute: Attribute[Any] | AttributeMap[Any],
        instance: str | None,
        expected: object,
        timeout: timedelta,
    ) -> None:
        encoded = self._values.encode(
            expected,
            self._values.codec(attribute.value_type),
        )
        if encoded.WhichOneof("kind") not in {
            "string_value",
            "bool_value",
            "int_value",
            "double_value",
        }:
            raise ValueError(
                "wait_for_attribute_equal supports only string, boolean, or number values"
            )
        await self._call(
            self._service.WaitForAttribute,
            pb.WaitForAttributeRequest(
                flow_id=require_name(flow_id),
                condition=pb.WaitForAttributeCondition(
                    equal=pb.WaitForAttributeEqual(
                        key=self._definition_name(attribute, instance),
                        value=encoded,
                    )
                ),
                wait_time_seconds=self._seconds32(timeout),
                request_id=str(uuid4()),
            ),
            "wait_for_attribute_equal",
            flow_id,
            "active",
        )

    async def update_flow_config(self, flow_id: str, config: FlowConfig) -> None:
        """Replace mutable configuration for an active Flow.

        Args:
            flow_id: The non-empty active Flow ID.
            config: New optional fields applied to later decisions.

        Raises:
            FlowNotActiveError: If the Flow is closed.
            ValueError: If a configuration value is invalid.
            DexServiceError: If FlowService cannot apply the update.
        """
        await self._call(
            self._service.UpdateFlowConfig,
            pb.UpdateFlowConfigRequest(
                flow_id=require_name(flow_id),
                flow_config=self._map_flow_config(config),
            ),
            "update_flow_config",
            flow_id,
            "active",
        )

    async def trigger_continue_as_new(self, flow_id: str) -> None:
        """Ask an active Flow to roll history into a new run.

        The Flow ID remains stable and the run ID changes when rollover occurs.

        Args:
            flow_id: The non-empty active Flow ID.

        Raises:
            FlowNotActiveError: If the Flow is closed.
            DexServiceError: If FlowService cannot accept the request.
        """
        await self._call(
            self._service.TriggerContinueAsNew,
            pb.TriggerContinueAsNewRequest(flow_id=require_name(flow_id)),
            "trigger_continue_as_new",
            flow_id,
            "active",
        )

    async def health_check(self) -> bool:
        """Await the FlowService health endpoint.

        Returns:
            ``True`` when the service returns successfully.

        Raises:
            DexServiceError: If the service is unhealthy or unreachable.
        """
        from google.protobuf import empty_pb2

        await self._call(
            self._service.HealthCheck,
            empty_pb2.Empty(),
            "health_check",
            None,
            "none",
        )
        return True

    async def close(self) -> None:
        """Close the owned asynchronous gRPC channel.

        ``close`` is idempotent. Other methods must not be awaited afterward. The
        Registry and BlobCache remain owned by the caller.
        """
        if self._closed:
            return
        self._closed = True
        await self._channel.close(None)

    async def _wait_for_flow_response(
        self,
        flow_id: str,
        timeout: timedelta | None,
    ) -> pb.FlowResult:
        request = pb.WaitForFlowRequest(
            flow_id=require_name(flow_id),
            needs_results=True,
        )
        if timeout is not None:
            request.wait_time_seconds = self._seconds32(timeout)
        response = cast(
            pb.FlowResult,
            await self._call(
                self._service.WaitForFlow,
                request,
                "wait_for_flow",
                flow_id,
                "existing",
            ),
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
            write = pb.AttributeWrite(
                key=key,
                value=self._values.encode(
                    initialization.value,
                    self._values.codec(definition.value_type),
                ),
            )
            _apply_attribute_store_sync(write, definition)
            mapped.attributes.append(write)
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
            if config.attribute_store_names is not None:
                mapped.attribute_store_names.CopyFrom(
                    pb.AttributeStoreNames(names=config.attribute_store_names)
                )
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
            mapped.initial_interval_seconds = AsyncClient._seconds32(
                retry.initial_interval
            )
        if retry.maximum_interval is not None:
            mapped.maximum_interval_seconds = AsyncClient._seconds32(
                retry.maximum_interval
            )
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
    def _attribute_wait_arguments(
        definition: Attribute[Any] | AttributeMap[Any],
        args: tuple[object, ...],
        kwargs: dict[str, object],
    ) -> tuple[str | None, object, timedelta]:
        parameter_names: tuple[str, ...]
        if isinstance(definition, Attribute):
            parameter_names = ("expected", "timeout")
        elif isinstance(definition, AttributeMap):
            parameter_names = ("instance", "expected", "timeout")
        else:
            raise TypeError("wait_for_attribute_equal received invalid arguments")
        if len(args) > len(parameter_names):
            raise TypeError("wait_for_attribute_equal received invalid arguments")
        arguments = dict(zip(parameter_names, args))
        for name, value in kwargs.items():
            if name not in parameter_names or name in arguments:
                raise TypeError("wait_for_attribute_equal received invalid arguments")
            arguments[name] = value
        if set(arguments) != set(parameter_names):
            raise TypeError("wait_for_attribute_equal received invalid arguments")
        timeout = arguments["timeout"]
        if not isinstance(timeout, timedelta):
            raise TypeError("wait_for_attribute_equal received invalid arguments")
        if isinstance(definition, Attribute):
            return None, arguments["expected"], timeout
        instance = arguments["instance"]
        if not isinstance(instance, str):
            raise TypeError("wait_for_attribute_equal received invalid arguments")
        return instance, arguments["expected"], timeout

    @staticmethod
    def _resolve_timeout_policy(
        registered: Any, options: StartFlowOptions
    ) -> pb.FlowTimeoutPolicy:
        policy = _resolve_flow_timeout_policy(
            registered.name,
            registered.has_timeout_handler,
            options.timeout,
            options.timeout_policy,
        )
        return {
            FlowTimeoutPolicy.DEFAULT: pb.FLOW_TIMEOUT_POLICY_UNSPECIFIED,
            FlowTimeoutPolicy.FAIL: pb.FLOW_TIMEOUT_POLICY_FAIL,
            FlowTimeoutPolicy.CANCEL: pb.FLOW_TIMEOUT_POLICY_CANCEL,
            FlowTimeoutPolicy.HANDLER: pb.FLOW_TIMEOUT_POLICY_HANDLER,
        }[policy]

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
            int(pb.FLOW_STATUS_SERVER_SIDE_TIMEOUT_INTERNAL_ONLY): (
                FlowStatus.SERVER_SIDE_TIMEOUT_INTERNAL_ONLY
            ),
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
            int(pb.FLOW_ERROR_TYPE_FLOW_TIMEOUT): FlowErrorType.FLOW_TIMEOUT,
        }
        return error_types.get(error_type)

    @staticmethod
    async def _call(
        method: Callable[[Any], Any],
        request: Any,
        operation: str,
        flow_id: str | None,
        requirement: FlowTargetRequirement,
    ) -> Any:
        try:
            return await method(request)
        except grpc.RpcError as error:
            raise translate_rpc_error(error, operation, flow_id, requirement) from error
