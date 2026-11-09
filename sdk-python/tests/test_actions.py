# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
import json
from typing import Any, cast

import pytest

from dex import (
    WORK_QUEUE_PERMISSIONS_INDEX_KEY,
    Action,
    AsyncClient,
    Attribute,
    AttributeMap,
    AttributeMatch,
    BlobCache,
    Client,
    Context,
    Flow,
    FlowDefinitionError,
    PersistenceSchema,
    Registry,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    SubFlowOptions,
    graceful_complete,
    rpc,
    when_attribute_matches,
)
from dex._async_worker_dispatcher import AsyncWorkerDispatcher
from dex._server_protocol import NegotiatedServerProtocol
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.dexpb import dex_pb2 as pb

CASE_STATUS = Attribute("case-status", str)
PRIORITY = Attribute("priority", int)
NOTE = Attribute("note", str)


class ReviewStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        if input == "status":
            CASE_STATUS.set(context, "awaiting-manager")
        else:
            NOTE.set(context, input)
        return graceful_complete()


class RefundFlow(Flow[str]):
    def __init__(self) -> None:
        self.review = ReviewStep()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.review)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(CASE_STATUS, PRIORITY, NOTE)

    @rpc(
        lock_attributes=(CASE_STATUS.lock(),),
        action=Action(
            "Approve refund",
            required_permission="refund.approve",
            condition=when_attribute_matches(
                CASE_STATUS,
                AttributeMatch.equal_to("awaiting-manager"),
                AttributeMatch.equal_to("escalated"),
            ),
        ),
    )
    def approve_refund(self, context: Context) -> None:
        CASE_STATUS.set(context, "approved")

    @rpc(
        action=Action(
            "Escalate",
            required_permission="refund.escalate",
            condition=when_attribute_matches(PRIORITY, AttributeMatch.equal_to(1)),
        ),
    )
    def escalate_refund(self, context: Context) -> None:
        NOTE.set(context, "escalated")

    @rpc(action=Action("Add note", required_permission="refund.note"))
    def add_note(self, context: Context, note: str) -> None:
        NOTE.set(context, note)


def _server_protocol() -> NegotiatedServerProtocol:
    server_protocol = NegotiatedServerProtocol()
    server_protocol.record_version(4)
    return server_protocol


class PassthroughHydrator:
    @staticmethod
    def execute_request(
        request: pb.InvokeExecuteMethodRequest,
    ) -> pb.InvokeExecuteMethodRequest:
        return request

    @staticmethod
    def rpc_request(request: pb.InvokeWorkerRPCRequest) -> pb.InvokeWorkerRPCRequest:
        return request


class AsyncPassthroughHydrator:
    @staticmethod
    async def rpc_request(
        request: pb.InvokeWorkerRPCRequest,
    ) -> pb.InvokeWorkerRPCRequest:
        return request


def _registry() -> tuple[Registry, RefundFlow]:
    flow = RefundFlow()
    return Registry((flow,)), flow


def _dispatcher() -> tuple[WorkerDispatcher, ValueMapper]:
    registry, _ = _registry()
    values = ValueMapper(registry.codec_registry)
    return (
        WorkerDispatcher(
            registry, values, cast(Any, PassthroughHydrator()), _server_protocol()
        ),
        values,
    )


def _expected_mappings(values: ValueMapper) -> list[tuple[str, list[pb.Value], str]]:
    string = values.codec(str)
    return [
        (
            "case-status",
            [
                values.encode("awaiting-manager", string),
                values.encode("escalated", string),
            ],
            "refund.approve",
        ),
        ("priority", [values.encode(1, values.codec(int))], "refund.escalate"),
    ]


def _mapping_tuples(
    mappings: pb.ActionPermissionMappings,
) -> list[tuple[str, list[pb.Value], str]]:
    return [
        (mapping.attribute_key, list(mapping.equal_values), mapping.required_permission)
        for mapping in mappings.mappings
    ]


def test_rpc_writing_a_condition_attribute_sends_every_conditioned_mapping() -> None:
    dispatcher, values = _dispatcher()
    response = dispatcher.invoke_rpc(
        pb.InvokeWorkerRPCRequest(flow_type="RefundFlow", rpc_name="approve_refund")
    )
    assert response.HasField("action_permission_mappings")
    assert _mapping_tuples(response.action_permission_mappings) == _expected_mappings(
        values
    )


def test_rpc_writing_only_unrelated_attributes_omits_mappings() -> None:
    dispatcher, values = _dispatcher()
    response = dispatcher.invoke_rpc(
        pb.InvokeWorkerRPCRequest(
            flow_type="RefundFlow",
            rpc_name="add_note",
            input=values.encode("hello", values.codec(str)),
        )
    )
    assert [write.key for write in response.upsert_attributes] == ["note"]
    assert not response.HasField("action_permission_mappings")


def test_execute_writing_a_condition_attribute_sends_mappings() -> None:
    dispatcher, values = _dispatcher()

    def execute(input: str) -> pb.InvokeExecuteMethodResponse:
        outputs = list(
            dispatcher.invoke_execute(
                pb.InvokeExecuteMethodRequest(
                    flow_type="RefundFlow",
                    step_type="ReviewStep",
                    step_input=values.encode(input, values.codec(str)),
                )
            )
        )
        return outputs[-1].result

    assert _mapping_tuples(
        execute("status").action_permission_mappings
    ) == _expected_mappings(values)
    assert not execute("other").HasField("action_permission_mappings")


def test_async_rpc_sends_mappings() -> None:
    flow = RefundFlow()
    registry = Registry((flow,), allow_async_handlers=True)
    values = ValueMapper(registry.codec_registry)
    dispatcher = AsyncWorkerDispatcher(
        registry, values, cast(Any, AsyncPassthroughHydrator()), _server_protocol()
    )
    response = asyncio.run(
        dispatcher.invoke_rpc(
            pb.InvokeWorkerRPCRequest(
                flow_type="RefundFlow", rpc_name="escalate_refund"
            )
        )
    )
    assert [write.key for write in response.upsert_attributes] == ["note"]
    assert not response.HasField("action_permission_mappings")
    approved = asyncio.run(
        dispatcher.invoke_rpc(
            pb.InvokeWorkerRPCRequest(flow_type="RefundFlow", rpc_name="approve_refund")
        )
    )
    assert _mapping_tuples(approved.action_permission_mappings) == _expected_mappings(
        values
    )


def test_registry_registers_the_work_queue_permissions_index() -> None:
    registry, _ = _registry()
    assert (
        registry._attribute_indexes[WORK_QUEUE_PERMISSIONS_INDEX_KEY]
        == pb.INDEX_TYPE_KEYWORD_ARRAY
    )
    assert (
        WORK_QUEUE_PERMISSIONS_INDEX_KEY
        not in Registry((PlainFlow(),))._attribute_indexes
    )


def _initial_permissions(
    start: pb.FlowStartOptions | pb.SubFlowOptions,
) -> list[pb.AttributeWrite]:
    return [
        write
        for write in start.attributes
        if write.key == WORK_QUEUE_PERMISSIONS_INDEX_KEY
    ]


def _assert_permissions_write(write: pb.AttributeWrite, permissions: list[str]) -> None:
    assert write.value.obj_value.encoding == "json"
    assert json.loads(write.value.obj_value.payload) == permissions
    assert (
        write.value.obj_value.payload
        == json.dumps(permissions, separators=(",", ":")).encode()
    )
    assert write.index_config == pb.IndexConfig(
        enable=True,
        type=pb.INDEX_TYPE_KEYWORD_ARRAY,
        index_key=WORK_QUEUE_PERMISSIONS_INDEX_KEY,
    )


def test_start_flow_writes_initial_permissions_sorted_and_deduplicated() -> None:
    registry, flow = _registry()
    registered = registry._flow_for_instance(flow)
    client = Client(registry, cast(BlobCache, object()))
    matching = client._map_start_options(
        registered,
        StartFlowOptions()
        .with_attribute(PRIORITY, 1)
        .with_attribute(CASE_STATUS, "escalated"),
    )
    (write,) = _initial_permissions(matching)
    _assert_permissions_write(write, ["refund.approve", "refund.escalate"])
    assert (
        matching.attributes[-1] is not None
        and matching.attributes[-1].key == WORK_QUEUE_PERMISSIONS_INDEX_KEY
    )

    not_matching = client._map_start_options(
        registered,
        StartFlowOptions()
        .with_attribute(CASE_STATUS, "approved")
        .with_attribute(PRIORITY, 2),
    )
    assert _initial_permissions(not_matching) == []
    assert (
        _initial_permissions(client._map_start_options(registered, StartFlowOptions()))
        == []
    )
    client.close()


def test_async_start_flow_writes_initial_permissions() -> None:
    registry, flow = _registry()

    async def map_start() -> pb.FlowStartOptions:
        client = AsyncClient(registry, cast(BlobCache, object()))
        return client._map_start_options(
            registry._flow_for_instance(flow),
            StartFlowOptions().with_attribute(CASE_STATUS, "awaiting-manager"),
        )

    (write,) = _initial_permissions(asyncio.run(map_start()))
    _assert_permissions_write(write, ["refund.approve"])


def test_sub_flow_start_writes_initial_permissions() -> None:
    dispatcher, _ = _dispatcher()
    target = dispatcher._registry._flow_by_type("RefundFlow")
    mapped = dispatcher._map_sub_flow_options(
        target, SubFlowOptions().with_attribute(PRIORITY, 1)
    )
    (write,) = _initial_permissions(mapped)
    _assert_permissions_write(write, ["refund.escalate"])


class PlainFlow(Flow[None]):
    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(CASE_STATUS)


def _flow_with_action(
    action: object, schema: PersistenceSchema | None = None
) -> Flow[None]:
    class ActionFlow(Flow[None]):
        def get_persistence_schema(self) -> PersistenceSchema:
            return schema or PersistenceSchema.of(CASE_STATUS)

        @rpc(action=cast(Action, action))
        def decide_case(self, context: Context) -> None:
            return None

    return ActionFlow()


@pytest.mark.parametrize(
    ("action", "message"),
    [
        (
            Action(" ", required_permission="refund.approve"),
            "Action label must not be empty",
        ),
        (Action("Approve", required_permission="Refund"), "must use lowercase letter"),
        (
            Action("Approve", required_permission="refund..approve"),
            "must use lowercase letter",
        ),
        (
            Action(
                "Approve",
                required_permission="refund.approve",
                condition=when_attribute_matches(CASE_STATUS),
            ),
            "requires at least one match",
        ),
        (
            Action(
                "Approve",
                required_permission="refund.approve",
                condition=when_attribute_matches(
                    CASE_STATUS, AttributeMatch.not_equal_to("x")
                ),
            ),
            "supports only AttributeMatch.equal_to",
        ),
        (
            Action(
                "Approve",
                required_permission="refund.approve",
                condition=when_attribute_matches(
                    Attribute("other", str), AttributeMatch.equal_to("x")
                ),
            ),
            "is not declared by the Flow",
        ),
        (
            Action(
                "Approve",
                required_permission="refund.approve",
                condition=when_attribute_matches(
                    Attribute("case-status", str, sync_to_attribute_store=True),
                    AttributeMatch.equal_to("x"),
                ),
            ),
            "does not match its registered definition",
        ),
        (
            Action(
                "Approve",
                required_permission="refund.approve",
                condition=when_attribute_matches(
                    cast(Attribute[str], AttributeMap("case-status", str)),
                    AttributeMatch.equal_to("x"),
                ),
            ),
            "not an AttributeMap",
        ),
        ("Approve", "must be a dex.Action"),
    ],
)
def test_registry_rejects_invalid_actions(action: object, message: str) -> None:
    with pytest.raises(FlowDefinitionError, match=message) as error:
        Registry((_flow_with_action(action),))
    assert "RPC decide_case Action" in str(error.value)


def test_registry_rejects_a_hard_delete_condition_attribute() -> None:
    secret = Attribute("secret", str, hard_delete_on_null=True)
    action = Action(
        "Approve",
        required_permission="refund.approve",
        condition=when_attribute_matches(secret, AttributeMatch.equal_to("x")),
    )
    with pytest.raises(FlowDefinitionError, match="uses hard_delete_on_null"):
        Registry((_flow_with_action(action, PersistenceSchema.of(secret)),))


def test_registry_rejects_a_non_finite_float_value() -> None:
    score = Attribute("score", float)
    action = Action(
        "Approve",
        required_permission="refund.approve",
        condition=when_attribute_matches(score, AttributeMatch.equal_to(float("inf"))),
    )
    with pytest.raises(FlowDefinitionError, match="non-finite"):
        Registry((_flow_with_action(action, PersistenceSchema.of(score)),))


def test_condition_less_action_registers_without_a_mapping() -> None:
    flow = _flow_with_action(Action("Decide", required_permission="case.decide"))
    registry = Registry((flow,))
    registered = registry._flow_for_instance(flow)
    assert (
        registered.action_permission_mappings_for_writes(
            [pb.AttributeWrite(key="case-status")]
        )
        is None
    )
    assert (
        registry._attribute_indexes[WORK_QUEUE_PERMISSIONS_INDEX_KEY]
        == pb.INDEX_TYPE_KEYWORD_ARRAY
    )
