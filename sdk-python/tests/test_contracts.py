# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Any, cast

import pytest

from dex import (
    INT64,
    STRING,
    Attribute,
    AttributeMap,
    BlobCache,
    BlobCacheConfig,
    Channel,
    ChannelMap,
    Client,
    CodecRegistry,
    ConditionCombination,
    Context,
    Flow,
    FlowConfig,
    FlowDefinitionError,
    InvalidStepResultError,
    JsonCodec,
    PersistenceSchema,
    Registry,
    RPCResult,
    StartFlowOptions,
    Step,
    StepDecision,
    StepDurability,
    StepList,
    StepOptions,
    Stream,
    Timer,
    ValueMappingError,
    Wait,
    WaitForFailurePolicy,
    WireKind,
    graceful_complete,
    open_blob_cache,
    rpc,
)
from dex._invocation_context import InvocationContext, InvocationMethod
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.client import Client as ClientModuleClient
from dex.client_options import ClientOptions as ClientModuleOptions
from dex.dexpb import dex_pb2 as pb


@dataclass(frozen=True)
class OrderInput:
    order_id: str


@dataclass(frozen=True)
class OrderOutput:
    accepted: bool


ORDER_INPUT = JsonCodec[OrderInput](
    "OrderInput",
    lambda value: OrderInput(order_id=value["order_id"]),
    lambda value: {"order_id": value.order_id},
)
STATUS = Attribute("status", str)
COMMANDS = Channel("commands", OrderInput)


class ApproveOrder(Step[OrderInput]):
    def get_step_type(self) -> str:
        return "ApproveOrder"

    def execute(self, context: Context, input: OrderInput) -> StepDecision:
        del context, input
        return graceful_complete()

    def wait_for(self, context: Context, input: OrderInput) -> Wait:
        del context, input
        return Wait.any_of(
            COMMANDS.for_one(),
            Timer.by_duration(
                timedelta(seconds=10),
                condition_id="approval-timeout",
            ),
        )


class ArchiveOrder(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        del context, input
        return graceful_complete()


class OrderFlow(Flow[OrderInput]):
    approve = ApproveOrder()
    archive = ArchiveOrder()

    def get_flow_type(self) -> str:
        return "Orders"

    def get_steps(self) -> StepList[OrderInput]:
        return StepList.start_step(self.approve).other_steps(self.archive)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(STATUS, COMMANDS)

    @rpc(name="GetOrder", lock_attributes=(STATUS.lock(),))
    def get_order(self, context: Context, input: OrderInput) -> RPCResult[OrderOutput]:
        del context, input
        return RPCResult(OrderOutput(accepted=True))


class AsyncApproveOrder(Step[OrderInput]):
    async def execute(  # type: ignore[override]
        self,
        context: Context,
        input: OrderInput,
    ) -> StepDecision:
        del context, input
        return graceful_complete()

    async def wait_for(  # type: ignore[override]
        self, context: Context, input: OrderInput
    ) -> Wait:
        del context, input
        return Wait.skip_immediately()


class AsyncOrderFlow(Flow[OrderInput]):
    approve = AsyncApproveOrder()

    def get_steps(self) -> StepList[OrderInput]:
        return StepList.start_step(self.approve)

    @rpc(name="GetOrderAsync")
    async def get_order(
        self,
        context: Context,
        input: OrderInput,
    ) -> RPCResult[OrderOutput]:
        del context, input
        return RPCResult(OrderOutput(accepted=True))


ORDERS = OrderFlow()


def test_typed_interfaces_construct_without_runtime() -> None:
    registry = Registry((ORDERS,))
    definitions = tuple(ORDERS.get_steps())
    assert registry.flows == (ORDERS,)
    assert definitions[0].step is ORDERS.approve
    assert definitions[0].is_start_step
    assert definitions[1].step is ORDERS.archive
    assert not definitions[1].is_start_step


def test_python_defaults_match_java_contracts() -> None:
    class Container:
        class NestedStep(Step[int]):
            def execute(self, context: Context, input: int) -> StepDecision:
                del context, input
                return graceful_complete()

    options = StepOptions()
    assert Container.NestedStep().get_step_type() == "NestedStep"
    assert OrderFlow().get_flow_type() == "Orders"
    assert options.wait_for_failure is WaitForFailurePolicy.FAIL_FLOW
    assert options.wait_for_durability is StepDurability.DEFAULT
    assert options.execute_durability is StepDurability.DEFAULT


def test_persistence_schema_groups_definition_types() -> None:
    items = AttributeMap("items", int)
    events = Stream("events", str, 1024)
    schema = PersistenceSchema.of(STATUS, items, COMMANDS, events)
    assert schema.attributes == (STATUS, items)
    assert schema.channels == (COMMANDS,)
    assert schema.streams == (events,)
    with pytest.raises(TypeError, match="unsupported persistence definition"):
        PersistenceSchema.of(cast(Any, object()))


def test_registry_infers_handler_codecs_from_annotations() -> None:
    registry = Registry((ORDERS,))
    input_codec = registry.codec_registry.resolve(OrderInput)
    output_codec = registry.codec_registry.resolve(OrderOutput)
    input = OrderInput("order-1")
    output = OrderOutput(True)
    assert input_codec.decode(input_codec.encode(input)) == input
    assert output_codec.decode(output_codec.encode(output)) == output


def test_registry_rejects_async_step_and_rpc_handlers() -> None:
    with pytest.raises(FlowDefinitionError, match="must be synchronous"):
        Registry((AsyncOrderFlow(),))


def test_registry_allows_async_handlers_when_enabled() -> None:
    registry = Registry((AsyncOrderFlow(),), allow_async_handlers=True)
    assert registry.flows[0].get_flow_type() == AsyncOrderFlow().get_flow_type()


def test_registry_rejects_duplicate_interfaces() -> None:
    with pytest.raises(FlowDefinitionError, match="duplicate Flow Orders"):
        Registry((ORDERS, ORDERS))


def test_registry_rejects_duplicate_step_classes() -> None:
    class DuplicateStep(Step[int]):
        def __init__(self, step_type: str) -> None:
            self._step_type = step_type

        def get_step_type(self) -> str:
            return self._step_type

        def execute(self, context: Context, input: int) -> StepDecision:
            del context
            return graceful_complete(input)

    class DuplicateStepFlow(Flow[int]):
        def get_steps(self) -> StepList[int]:
            return StepList.start_step(DuplicateStep("first")).other_steps(
                DuplicateStep("second")
            )

    with pytest.raises(FlowDefinitionError, match="duplicate Step class"):
        Registry((DuplicateStepFlow(),))


def test_registry_wraps_user_definition_failures_with_flow_context() -> None:
    class BrokenFlow(Flow[None]):
        def get_steps(self) -> StepList[None]:
            raise RuntimeError("cannot assemble steps")

    with pytest.raises(
        FlowDefinitionError,
        match="Flow BrokenFlow registration failed: cannot assemble steps",
    ):
        Registry((BrokenFlow(),))


def test_registry_rejects_invalid_handler_signatures() -> None:
    class WrongContextStep(Step[int]):
        def execute(self, context: object, input: int) -> StepDecision:
            del context, input
            return graceful_complete()

    class WrongContextFlow(Flow[int]):
        start = WrongContextStep()

        def get_steps(self) -> StepList[int]:
            return StepList.start_step(self.start)

    with pytest.raises(FlowDefinitionError, match="context must be Context"):
        Registry((WrongContextFlow(),))


def test_registry_rejects_mismatched_wait_for_input() -> None:
    class MismatchedStep(Step[int]):
        def wait_for(self, context: Context, input: str) -> Wait:  # type: ignore[override]
            del context, input
            return Wait.skip_immediately()

        def execute(self, context: Context, input: int) -> StepDecision:
            del context, input
            return graceful_complete()

    class MismatchedFlow(Flow[int]):
        start = MismatchedStep()

        def get_steps(self) -> StepList[int]:
            return StepList.start_step(self.start)

    with pytest.raises(
        FlowDefinitionError, match="handlers must use the same input type"
    ):
        Registry((MismatchedFlow(),))


def test_registry_rejects_duplicate_rpc_locks() -> None:
    locked = Attribute("locked", str)

    class DuplicateLockFlow(Flow[None]):
        def get_persistence_schema(self) -> PersistenceSchema:
            return PersistenceSchema.of(locked)

        @rpc(lock_attributes=(locked.lock(), locked.lock()))
        def update(self, context: Context) -> None:
            del context

    with pytest.raises(FlowDefinitionError, match="duplicate attribute lock"):
        Registry((DuplicateLockFlow(),))


def test_value_mapping_errors_are_stable() -> None:
    client = ClientModuleClient(Registry((ORDERS,)), cast(BlobCache, object()))
    with pytest.raises(ValueMappingError, match="Cannot encode Dex Value"):
        client._values.encode(cast(OrderInput, object()), ORDER_INPUT)


def test_invalid_step_result_has_flow_and_step_context() -> None:
    class InvalidStep(Step[int]):
        def execute(self, context: Context, input: int) -> StepDecision:
            del context, input
            return cast(StepDecision, None)

    class InvalidFlow(Flow[int]):
        start = InvalidStep()

        def get_steps(self) -> StepList[int]:
            return StepList.start_step(self.start)

    class PassthroughHydrator:
        @staticmethod
        def execute_request(
            request: pb.InvokeExecuteMethodRequest,
        ) -> pb.InvokeExecuteMethodRequest:
            return request

    registry = Registry((InvalidFlow(),))
    values = ValueMapper(registry.codec_registry)
    dispatcher = WorkerDispatcher(
        registry,
        values,
        cast(Any, PassthroughHydrator()),
        lambda _request: pb.WriteStreamRequest(),
    )
    request = pb.InvokeExecuteMethodRequest(
        flow_type="InvalidFlow",
        step_type="InvalidStep",
        step_input=values.encode(1, values.codec(int)),
    )
    with pytest.raises(InvalidStepResultError) as captured:
        dispatcher.invoke_execute(request)
    assert captured.value.flow_type == "InvalidFlow"
    assert captured.value.step_type == "InvalidStep"
    assert captured.value.method == "execute"


def test_builtin_codecs_enforce_wire_types_and_ranges() -> None:
    encoded = INT64.encode(42)
    assert encoded.kind is WireKind.INT64
    assert INT64.decode(encoded) == 42
    with pytest.raises(OverflowError):
        INT64.encode(2**63)
    with pytest.raises(TypeError):
        INT64.decode(STRING.encode("42"))


def test_fluent_wait_factories_validate_channel_bounds() -> None:
    wait = Wait.until(Timer.by_duration(timedelta(seconds=1)))
    assert len(wait.conditions) == 1
    with pytest.raises(ValueError, match="requires a bound"):
        COMMANDS.for_range()


def test_worker_maps_only_user_provided_condition_ids() -> None:
    channel = Channel("condition-commands", str)

    class ConditionStep(Step[str]):
        def wait_for(self, context: Context, input: str) -> Wait:
            del context
            if input == "unnamed":
                return Wait.any_of(
                    Timer.by_duration(timedelta(seconds=1)), channel.for_one()
                )
            if input == "missing":
                return Wait.any_combination_of(
                    ConditionCombination.of(channel.for_one())
                )
            if input == "duplicate":
                return Wait.any_combination_of(
                    ConditionCombination.of(
                        channel.for_one(condition_id="same"),
                        Timer.by_duration(timedelta(seconds=1), condition_id="same"),
                    )
                )
            reused = channel.for_one(condition_id="__dex_internal_condition_0")
            return Wait.any_combination_of(
                ConditionCombination.of(reused),
                ConditionCombination.of(reused),
            )

        def execute(self, context: Context, input: str) -> StepDecision:
            del context
            return graceful_complete(input)

    class ConditionFlow(Flow[str]):
        start = ConditionStep()

        def get_steps(self) -> StepList[str]:
            return StepList.start_step(self.start)

        def get_persistence_schema(self) -> PersistenceSchema:
            return PersistenceSchema.of(channel)

    class PassthroughHydrator:
        @staticmethod
        def wait_for_request(
            request: pb.InvokeWaitForMethodRequest,
        ) -> pb.InvokeWaitForMethodRequest:
            return request

    registry = Registry((ConditionFlow(),))
    values = ValueMapper(registry.codec_registry)
    dispatcher = WorkerDispatcher(
        registry,
        values,
        cast(Any, PassthroughHydrator()),
        lambda _request: pb.WriteStreamRequest(),
    )

    def invoke(input: str) -> pb.InvokeWaitForMethodResponse:
        return dispatcher.invoke_wait_for(
            pb.InvokeWaitForMethodRequest(
                flow_type="ConditionFlow",
                step_type="ConditionStep",
                step_input=values.encode(input, values.codec(str)),
            )
        )

    unnamed = invoke("unnamed").waiting_condition
    assert unnamed.timer_conditions[0].condition_id == ""
    assert unnamed.channel_conditions[0].condition_id == ""
    reused = invoke("reused").waiting_condition
    assert reused.channel_conditions[0].condition_id == "__dex_internal_condition_0"
    assert [list(item.condition_ids) for item in reused.condition_combinations] == [
        ["__dex_internal_condition_0"],
        ["__dex_internal_condition_0"],
    ]
    with pytest.raises(InvalidStepResultError, match="requires every Condition"):
        invoke("missing")
    with pytest.raises(InvalidStepResultError, match="duplicate Condition ID"):
        invoke("duplicate")
    with pytest.raises(ValueError, match="must not be empty"):
        channel.for_one(condition_id="")


def test_map_introspection_tracks_buffered_changes() -> None:
    attributes = AttributeMap("items", str)
    channels = ChannelMap("messages", str)

    class MapFlow(Flow[None]):
        def get_persistence_schema(self) -> PersistenceSchema:
            return PersistenceSchema.of(attributes, channels)

    registry = Registry((MapFlow(),))
    values = ValueMapper(registry.codec_registry)
    special = "special / key"
    context = InvocationContext(
        InvocationMethod.RPC,
        registry._flow_by_type("MapFlow"),
        pb.Context(),
        values,
        lambda _request: pb.WriteStreamRequest(),
        (
            pb.KV(
                key=Registry.physical_name("items", special),
                value=values.encode("initial", values.codec(str)),
            ),
            pb.KV(
                key=Registry.physical_name("items", "z"),
                value=values.encode("remove", values.codec(str)),
            ),
        ),
        channel_infos={
            Registry.physical_name("messages", special): pb.ChannelInfo(size=1),
            Registry.physical_name("messages", "empty"): pb.ChannelInfo(size=0),
        },
    )
    assert attributes.get_all_instance_keys(context) == (special, "z")
    attributes.set(context, "a", "added")
    attributes.delete(context, "z")
    assert attributes.get_all_instance_keys(context) == ("a", special)
    assert attributes.get_map_size(context) == 2
    assert channels.get_all_instance_keys(context) == (special,)
    channels.publish(context, "a", "published")
    assert channels.get_all_instance_keys(context) == ("a", special)
    assert channels.get_map_size(context) == 2


def test_explicit_custom_codec_remains_available() -> None:
    codecs = CodecRegistry({OrderInput: ORDER_INPUT})
    assert codecs.resolve(OrderInput) is ORDER_INPUT


def test_blob_cache_contract_matches_core_config(tmp_path: Any) -> None:
    config = BlobCacheConfig(str(tmp_path), 1024, 0)
    assert config.frequency_counters == 10_000
    cache = open_blob_cache(config)
    assert cache.put("blob", b"payload")
    assert cache.get("blob") == b"payload"
    cache.delete("blob")
    assert cache.get("blob") is None
    cache.close()


def test_client_has_single_public_definition() -> None:
    assert Client is ClientModuleClient
    client = ClientModuleClient(Registry((ORDERS,)), cast(BlobCache, object()))
    assert client.options == ClientModuleOptions()


def test_client_transport_is_initialized_lazily() -> None:
    client = Client(Registry((ORDERS,)), cast(BlobCache, object()))
    client.close()


def test_attribute_store_sync_mapping_preserves_presence() -> None:
    plain = Attribute("plain", str)
    synced = Attribute("synced", str, sync_to_attribute_store=True)
    synced_map = AttributeMap("mapped", str, sync_to_attribute_store=True)
    assert not plain.sync_to_attribute_store
    assert synced.sync_to_attribute_store
    assert synced_map.sync_to_attribute_store

    client = ClientModuleClient(Registry((ORDERS,)), cast(BlobCache, object()))
    start = client._map_start_options(
        StartFlowOptions()
        .with_attribute(plain, "plain")
        .with_attribute(synced, "synced")
        .with_attribute(synced_map, "tenant-1", "mapped")
    )
    assert not start.attributes[0].HasField("sync_config")
    assert start.attributes[1].sync_config.enabled
    assert start.attributes[2].sync_config.enabled

    absent = client._map_flow_config(FlowConfig())
    assert not absent.HasField("attribute_store_names")
    selected = client._map_flow_config(
        FlowConfig(attribute_store_names=["reporting", "audit"])
    )
    assert list(selected.attribute_store_names.names) == ["reporting", "audit"]
    assert selected.HasField("attribute_store_names")
    disabled = client._map_flow_config(FlowConfig(attribute_store_names=[]))
    assert list(disabled.attribute_store_names.names) == []
    assert disabled.HasField("attribute_store_names")
    client.close()


def test_step_stream_write_uses_execution_idempotency_key() -> None:
    thinking = Stream("thinking", str, 1_048_576)

    class StreamFlow(Flow[str]):
        def get_persistence_schema(self) -> PersistenceSchema:
            return PersistenceSchema.of(thinking)

    registry = Registry((StreamFlow(),))
    values = ValueMapper(registry.codec_registry)
    requests: list[pb.WriteStreamRequest] = []
    context = InvocationContext(
        InvocationMethod.EXECUTE,
        registry._flow_by_type("StreamFlow"),
        pb.Context(
            flow_id="flow-1",
            run_id="run-1",
            step_execution_id="step-1",
        ),
        values,
        requests.append,
        (),
    )

    assert thinking.write(context, "checking") is None
    assert len(requests) == 1
    request = requests[0]
    assert request.flow_id == "flow-1"
    assert request.flow_type == "StreamFlow"
    assert request.stream_name == "thinking"
    assert request.max_estimated_bytes == 1_048_576
    assert request.idempotency_key == "run-1#step-1"
    assert values.decode(request.value, values.codec(str)) == "checking"
    with pytest.raises(ValueError, match="already written"):
        thinking.write(context, "again")

    rpc_context = InvocationContext(
        InvocationMethod.RPC,
        registry._flow_by_type("StreamFlow"),
        pb.Context(),
        values,
        requests.append,
        (),
    )
    with pytest.raises(ValueError, match="Step Context"):
        thinking.write(rpc_context, "rejected")


def test_client_stream_transport_and_metadata() -> None:
    thinking = Stream("thinking", str, 1_048_576)

    class StreamFlow(Flow[str]):
        def get_persistence_schema(self) -> PersistenceSchema:
            return PersistenceSchema.of(thinking)

    class StreamService:
        def __init__(self) -> None:
            self.write_request: pb.WriteStreamRequest | None = None
            self.read_request: pb.ReadStreamRequest | None = None

        def WriteStream(self, request: pb.WriteStreamRequest) -> object:
            self.write_request = request
            return object()

        def ReadStream(self, request: pb.ReadStreamRequest) -> pb.ReadStreamResponse:
            self.read_request = request
            created_time = datetime(2026, 8, 27, 12, tzinfo=timezone.utc)
            response = pb.ReadStreamResponse()
            response.message.value.string_value = "working"
            response.message.resume_token = "resume-1"
            response.message.created_time.FromDatetime(created_time)
            response.message.idempotency_key = "client-1"
            return response

    client = Client(Registry((StreamFlow(),)), cast(BlobCache, object()))
    service = StreamService()
    client._service = cast(Any, service)
    try:
        client.write_stream("flow-1", thinking, "client-1", "starting")
        message = client.read_stream(
            "flow-1",
            thinking,
            "previous",
            timedelta(seconds=2),
        )
        assert service.write_request is not None
        assert service.write_request.flow_type == "StreamFlow"
        assert service.write_request.stream_name == "thinking"
        assert service.write_request.max_estimated_bytes == 1_048_576
        assert service.write_request.idempotency_key == "client-1"
        assert service.read_request is not None
        assert service.read_request.resume_token == "previous"
        assert service.read_request.wait_time_seconds == 2
        assert message.value == "working"
        assert message.resume_token == "resume-1"
        assert message.created_time == datetime(2026, 8, 27, 12, tzinfo=timezone.utc)
        assert message.idempotency_key == "client-1"
        with pytest.raises(ValueError, match="must not contain"):
            client.write_stream("flow-1", thinking, "bad#key", "ignored")
    finally:
        client.close()
