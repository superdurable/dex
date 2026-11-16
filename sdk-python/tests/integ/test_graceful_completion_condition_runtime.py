# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from datetime import timedelta

from dex import FlowStatus, StopFlowOptions, StopType
from dex._server_protocol import GRACEFUL_COMPLETION_CONDITION_PROTOCOL_VERSION

from .environment import DexDevTestEnvironment
from .graceful_completion_condition_flow import (
    DIRECT_INPUT,
    GATE_INPUT,
    GracefulCompletionConditionFlow,
)
from .shared import unique_id


def test_graceful_stop_wakes_a_step_waiting_on_the_graceful_condition() -> None:
    flow = GracefulCompletionConditionFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("python-graceful-condition-wake")
        environment.client.start_flow(flow, flow_id, DIRECT_INPUT)
        assert flow.wait_registered.wait(timeout=10)
        assert (
            environment.client._server_protocol.version
            >= GRACEFUL_COMPLETION_CONDITION_PROTOCOL_VERSION
        )

        environment.client.stop_flow(
            flow_id, StopFlowOptions(type=StopType.GRACEFUL_COMPLETE)
        )

        result = environment.client.wait_for_flow(flow_id, timedelta(seconds=15))
        assert result.status is FlowStatus.COMPLETED
        assert flow.observations == [((), True)]


def test_message_published_before_the_stop_is_consumed_first() -> None:
    flow = GracefulCompletionConditionFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("python-graceful-condition-message")
        environment.client.start_flow(flow, flow_id, GATE_INPUT)
        environment.client.invoke_rpc(flow.send_message, flow_id, "hello")
        environment.client.stop_flow(
            flow_id, StopFlowOptions(type=StopType.GRACEFUL_COMPLETE)
        )
        assert environment.client.describe_flow(flow_id).status is FlowStatus.RUNNING

        environment.client.invoke_rpc(flow.open_gate, flow_id)

        result = environment.client.wait_for_flow(flow_id, timedelta(seconds=15))
        assert result.status is FlowStatus.COMPLETED
        assert flow.observations == [(("hello",), True), ((), True)]
