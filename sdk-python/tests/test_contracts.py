# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from dataclasses import dataclass
from datetime import timedelta
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
    Client,
    CodecRegistry,
    Context,
    Flow,
    JsonCodec,
    PersistenceSchema,
    PhaseNotImplementedError,
    Registry,
    RPCResult,
    Step,
    StepList,
    StepDecision,
    StepDurability,
    StepOptions,
    Timer,
    Wait,
    WireKind,
    WaitForFailurePolicy,
    graceful_complete,
    open_blob_cache,
    rpc,
)


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
    async def execute(
        self,
        context: Context,
        input: OrderInput,
    ) -> StepDecision:
        del context, input
        return graceful_complete()

    async def wait_for(self, context: Context, input: OrderInput) -> Wait:
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
    assert AsyncOrderFlow().get_flow_type() == "AsyncOrderFlow"
    assert options.wait_for_failure is WaitForFailurePolicy.FAIL_FLOW
    assert options.wait_for_durability is StepDurability.DEFAULT
    assert options.execute_durability is StepDurability.DEFAULT


def test_persistence_schema_groups_definition_types() -> None:
    items = AttributeMap("items", int)
    schema = PersistenceSchema.of(STATUS, items, COMMANDS)
    assert schema.attributes == (STATUS, items)
    assert schema.channels == (COMMANDS,)
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


def test_registry_accepts_async_step_and_rpc_handlers() -> None:
    registry = Registry((AsyncOrderFlow(),))
    assert len(registry.flows) == 1


def test_registry_rejects_duplicate_interfaces() -> None:
    with pytest.raises(ValueError, match="duplicate Flow Orders"):
        Registry((ORDERS, ORDERS))


def test_registry_rejects_invalid_handler_signatures() -> None:
    class WrongContextStep(Step[int]):
        def execute(self, context: object, input: int) -> StepDecision:
            del context, input
            return graceful_complete()

    class WrongContextFlow(Flow[int]):
        start = WrongContextStep()

        def get_steps(self) -> StepList[int]:
            return StepList.start_step(self.start)

    with pytest.raises(TypeError, match="context must be Context"):
        Registry((WrongContextFlow(),))


def test_registry_rejects_mismatched_wait_for_input() -> None:
    class MismatchedStep(Step[int]):
        def wait_for(self, context: Context, input: str) -> Wait:
            del context, input
            return Wait.skip_immediately()

        def execute(self, context: Context, input: int) -> StepDecision:
            del context, input
            return graceful_complete()

    class MismatchedFlow(Flow[int]):
        start = MismatchedStep()

        def get_steps(self) -> StepList[int]:
            return StepList.start_step(self.start)

    with pytest.raises(TypeError, match="handlers must use the same input type"):
        Registry((MismatchedFlow(),))


def test_registry_rejects_duplicate_rpc_locks() -> None:
    locked = Attribute("locked", str)

    class DuplicateLockFlow(Flow[None]):
        def get_persistence_schema(self) -> PersistenceSchema:
            return PersistenceSchema.of(locked)

        @rpc(lock_attributes=(locked.lock(), locked.lock()))
        def update(self, context: Context) -> None:
            del context

    with pytest.raises(ValueError, match="duplicate attribute lock"):
        Registry((DuplicateLockFlow(),))


def test_builtin_codecs_enforce_wire_types_and_ranges() -> None:
    encoded = INT64.encode(42)
    assert encoded.kind is WireKind.INT64
    assert INT64.decode(encoded) == 42
    with pytest.raises(OverflowError):
        INT64.encode(2**63)
    with pytest.raises(TypeError):
        INT64.decode(STRING.encode("42"))


def test_fluent_wait_factories_validate_channel_bounds() -> None:
    wait = Wait.all_of(Timer.by_duration(timedelta(seconds=1)))
    assert len(wait.conditions) == 1
    with pytest.raises(ValueError, match="requires a bound"):
        COMMANDS.for_range()


def test_explicit_custom_codec_remains_available() -> None:
    codecs = CodecRegistry({OrderInput: ORDER_INPUT})
    assert codecs.resolve(OrderInput) is ORDER_INPUT


def test_blob_cache_contract_matches_core_config() -> None:
    config = BlobCacheConfig("contract-cache", 1024, 0)
    assert config.frequency_counters == 10_000
    with pytest.raises(PhaseNotImplementedError):
        open_blob_cache(config)


def test_transport_contract_fails_explicitly() -> None:
    client = Client(Registry((ORDERS,)), cast(BlobCache, object()))
    with pytest.raises(PhaseNotImplementedError):
        client.start_flow(ORDERS, "order-1", OrderInput("order-1"))
