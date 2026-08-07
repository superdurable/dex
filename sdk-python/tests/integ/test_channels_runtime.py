# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta

from dex import StepExecutionId, TimerId

from .basic_internal_channel_flow import BasicInternalChannelFlow
from .conditional_complete_flow import ConditionalCompleteFlow
from .environment import DexDevTestEnvironment
from .signal_flow import SignalFlow
from .test_basic_runtime import unique_id
from .waiting_internal_channel_flow import WaitingInternalChannelFlow

WAIT_TIMEOUT = timedelta(seconds=30)


def test_conditional_complete_with_signal_channel() -> None:
    flow = ConditionalCompleteFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("conditional-signal")
        environment.client.start_flow(flow, flow_id, True)
        environment.client.publish(flow_id, flow.signal, None)
        assert environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 1


def test_conditional_complete_with_internal_channel() -> None:
    flow = ConditionalCompleteFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("conditional-internal")
        environment.client.start_flow(flow, flow_id, False)
        environment.client.invoke_rpc(flow.publish_to_internal_channel, flow_id)
        assert environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 1


def test_basic_internal_channel() -> None:
    flow = BasicInternalChannelFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("basic-internal")
        environment.client.start_flow(flow, flow_id, 1)
        assert environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 2


def test_waiting_internal_channel() -> None:
    flow = WaitingInternalChannelFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("waiting-internal")
        environment.client.start_flow(flow, flow_id, 1)
        environment.client.publish(flow_id, flow.channel, 2, 3)
        assert environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 6


def test_signal_conditions_and_timer_skip() -> None:
    flow = SignalFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("basic-signal")
        environment.client.start_flow(flow, flow_id, 1)
        environment.client.publish(flow_id, flow.first, 2, 3, 5)
        environment.client.publish(flow_id, flow.third, None)
        environment.client.publish(flow_id, flow.signal_map, "one", 4)
        environment.client.skip_timer(
            flow_id,
            StepExecutionId("SignalCombinationStep"),
            TimerId.by_condition_id("test-timer-id"),
        )
        assert environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 6
