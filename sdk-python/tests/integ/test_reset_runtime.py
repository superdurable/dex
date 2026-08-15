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

import pytest

from dex import (
    FlowStatus,
    ResetFlowOptions,
    ResetType,
    StartFlowOptions,
)

from .environment import DexDevTestEnvironment
from .reset_flow import ResetFlow
from .shared import unique_id


@pytest.mark.parametrize("locking", (True, False))
def test_reset_reapplies_rpc_or_channel(locking: bool) -> None:
    flow = ResetFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = start_and_invoke(environment, flow, locking)
        assert_completed_with_attributes(environment, flow, flow_id)
        reset_run_id = environment.client.reset_flow(
            flow_id,
            reset_options(skip_writes=False),
        )
        assert_completed_with_attributes(environment, flow, flow_id)
        assert environment.client.describe_flow(flow_id).run_id == reset_run_id


@pytest.mark.parametrize("locking", (True, False))
def test_reset_can_skip_rpc_or_channel_reapply(locking: bool) -> None:
    flow = ResetFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = start_and_invoke(environment, flow, locking)
        assert_completed_with_attributes(environment, flow, flow_id)
        environment.client.reset_flow(
            flow_id,
            reset_options(skip_writes=True),
        )
        failure = environment.client.wait_for_flow(
            flow_id,
            timedelta(seconds=10),
        )
        assert failure.status is FlowStatus.TIMED_OUT
        assert len(failure.completions) == 0
        assert environment.client.get_attribute(flow_id, flow.data) is None
        assert environment.client.get_attribute(flow_id, flow.keyword) is None
        assert environment.client.get_attribute(flow_id, flow.counter) is None


def start_and_invoke(
    environment: DexDevTestEnvironment,
    flow: ResetFlow,
    locking: bool,
) -> str:
    flow_id = unique_id("reset")
    environment.client.start_flow(
        flow,
        flow_id,
        None,
        StartFlowOptions(timeout=timedelta(seconds=3)),
    )
    method = flow.with_locking if locking else flow.without_locking
    environment.client.invoke_rpc(method, flow_id)
    return flow_id


def reset_options(
    *,
    skip_writes: bool,
) -> ResetFlowOptions:
    return ResetFlowOptions(
        ResetType.BEGINNING,
        reason="testing reset",
        skip_writes_reapply=skip_writes,
    )


def assert_completed_with_attributes(
    environment: DexDevTestEnvironment,
    flow: ResetFlow,
    flow_id: str,
) -> None:
    assert (
        environment.client.wait_for_flow(flow_id, timedelta(seconds=10)).single_output(
            str
        )
        == "lock complete"
    )
    assert environment.client.describe_flow(flow_id).status is FlowStatus.COMPLETED
    assert environment.client.get_attribute(flow_id, flow.data) == flow.EXPECTED_VALUE
    assert (
        environment.client.get_attribute(flow_id, flow.keyword) == flow.EXPECTED_VALUE
    )
    assert environment.client.get_attribute(flow_id, flow.counter) == 100
