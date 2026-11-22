# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

import asyncio
from datetime import timedelta
from uuid import uuid4

import pytest

from dex import (
    FlowNotActiveOrNotFoundError,
    RPCInvokeOptions,
    StopFlowOptions,
    StopType,
)

from .async_environment import AsyncDexDevTestEnvironment
from .environment import DexDevTestEnvironment
from .rpc_request_id_flow import RpcRequestIdFlow
from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)


def _event_options() -> RPCInvokeOptions:
    return RPCInvokeOptions(request_id=f"event-{uuid4()}")


def test_transactional_rpc_repeated_with_one_request_id_applies_once() -> None:
    asyncio.run(_transactional_rpc_repeated_with_one_request_id_applies_once())


async def _transactional_rpc_repeated_with_one_request_id_applies_once() -> None:
    flow = RpcRequestIdFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("rpc-request-id-repeat")
        await client.start_flow(flow, flow_id, None)
        options = _event_options()
        assert (
            await client.invoke_rpc(flow.add_transactional, flow_id, 5, options=options)
            == 5
        )
        assert (
            await client.invoke_rpc(flow.add_transactional, flow_id, 5, options=options)
            == 5
        )
        assert await client.invoke_rpc(flow.get_total, flow_id) == 5
        # A recorded request ID is answered before dispatch, so the handler ran once.
        assert flow.handler_runs(flow_id, "add_transactional") == 1
        await client.stop_flow(flow_id)


def test_sync_client_repeat_with_one_request_id_applies_once() -> None:
    flow = RpcRequestIdFlow()
    with DexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("rpc-request-id-sync")
        client.start_flow(flow, flow_id, None)
        options = _event_options()
        for _ in range(2):
            assert (
                client.invoke_rpc(flow.add_transactional, flow_id, 3, options=options)
                == 3
            )
        assert client.invoke_rpc(flow.add_transactional, flow_id, 3) == 6
        assert client.invoke_rpc(flow.get_total, flow_id) == 6
        assert flow.handler_runs(flow_id, "add_transactional") == 2
        client.stop_flow(flow_id)


def test_request_id_reused_with_different_input_returns_the_first_reply() -> None:
    asyncio.run(_request_id_reused_with_different_input_returns_the_first_reply())


async def _request_id_reused_with_different_input_returns_the_first_reply() -> None:
    flow = RpcRequestIdFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("rpc-request-id-reused")
        await client.start_flow(flow, flow_id, None)
        options = _event_options()
        assert (
            await client.invoke_rpc(flow.add_transactional, flow_id, 5, options=options)
            == 5
        )
        # Dex compares only the request ID, so the changed input gets the first reply.
        assert (
            await client.invoke_rpc(flow.add_transactional, flow_id, 7, options=options)
            == 5
        )
        assert await client.invoke_rpc(flow.get_total, flow_id) == 5
        assert flow.handler_runs(flow_id, "add_transactional") == 1
        await client.stop_flow(flow_id)


def test_rpc_calls_without_a_request_id_apply_every_time() -> None:
    asyncio.run(_rpc_calls_without_a_request_id_apply_every_time())


async def _rpc_calls_without_a_request_id_apply_every_time() -> None:
    flow = RpcRequestIdFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("rpc-request-id-absent")
        await client.start_flow(flow, flow_id, None)
        assert await client.invoke_rpc(flow.add_transactional, flow_id, 3) == 3
        assert (
            await client.invoke_rpc(
                flow.add_transactional, flow_id, 3, options=RPCInvokeOptions()
            )
            == 6
        )
        assert await client.invoke_rpc(flow.get_total, flow_id) == 6
        assert flow.handler_runs(flow_id, "add_transactional") == 2
        with pytest.raises(ValueError, match="request_id must not be empty"):
            RPCInvokeOptions(request_id="")
        await client.stop_flow(flow_id)


def test_non_transactional_repeat_reruns_the_handler_and_applies_once() -> None:
    asyncio.run(_non_transactional_repeat_reruns_the_handler_and_applies_once())


async def _non_transactional_repeat_reruns_the_handler_and_applies_once() -> None:
    flow = RpcRequestIdFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("rpc-request-id-non-transactional")
        await client.start_flow(flow, flow_id, None)
        options = _event_options()
        for _ in range(2):
            assert (
                await client.invoke_rpc(
                    flow.add_non_transactional, flow_id, 4, options=options
                )
                == 4
            )
        assert await client.invoke_rpc(flow.get_total, flow_id) == 4
        assert flow.handler_runs(flow_id, "add_non_transactional") == 2
        await client.stop_flow(flow_id)


def test_zero_effect_repeat_returns_the_recorded_reply_only_when_transactional() -> (
    None
):
    asyncio.run(
        _zero_effect_repeat_returns_the_recorded_reply_only_when_transactional()
    )


async def _zero_effect_repeat_returns_the_recorded_reply_only_when_transactional() -> (
    None
):
    flow = RpcRequestIdFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        non_transactional_flow_id = unique_id("rpc-request-id-zero-effect")
        await client.start_flow(flow, non_transactional_flow_id, None)
        options = _event_options()
        replies = [
            await client.invoke_rpc(
                flow.mark_processed_non_transactional,
                non_transactional_flow_id,
                options=options,
            )
            for _ in range(2)
        ]
        assert replies == ["accepted", "already_processed"]
        await client.stop_flow(non_transactional_flow_id)

        transactional_flow_id = unique_id("rpc-request-id-zero-effect-transactional")
        await client.start_flow(flow, transactional_flow_id, None)
        options = _event_options()
        replies = [
            await client.invoke_rpc(
                flow.mark_processed_transactional,
                transactional_flow_id,
                options=options,
            )
            for _ in range(2)
        ]
        assert replies == ["accepted", "accepted"]
        await client.stop_flow(transactional_flow_id)


def test_transactional_repeat_after_the_flow_closes_returns_the_recorded_reply() -> (
    None
):
    asyncio.run(
        _transactional_repeat_after_the_flow_closes_returns_the_recorded_reply()
    )


async def _transactional_repeat_after_the_flow_closes_returns_the_recorded_reply() -> (
    None
):
    flow = RpcRequestIdFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("rpc-request-id-closed")
        await client.start_flow(flow, flow_id, None)
        options = _event_options()
        assert (
            await client.invoke_rpc(flow.add_transactional, flow_id, 7, options=options)
            == 7
        )
        await client.stop_flow(
            flow_id,
            StopFlowOptions(StopType.TERMINATE, "close before the retry"),
        )
        await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert (
            await client.invoke_rpc(flow.add_transactional, flow_id, 7, options=options)
            == 7
        )
        assert flow.handler_runs(flow_id, "add_transactional") == 1
        with pytest.raises(FlowNotActiveOrNotFoundError):
            await client.invoke_rpc(
                flow.add_transactional, flow_id, 7, options=_event_options()
            )
        assert flow.handler_runs(flow_id, "add_transactional") == 1
