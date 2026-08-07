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

from dex import Client, StartFlowOptions, StopFlowOptions, StopType

from .empty_decision_flow import EmptyDecisionFlow
from .force_fail_flow import ForceFailFlow
from .signal_flow import SignalFlow
from .state_failure_flow import StateFailureFlow
from .state_timeout_flow import StateTimeoutFlow


def compile_wait_and_flow_timeouts(client: Client) -> None:
    options = StartFlowOptions(timeout=timedelta(seconds=1))
    client.start_flow(SignalFlow(), "wait-timeout", 0, options)
    output: int = client.wait_for_flow(
        "wait-timeout",
        int,
        timedelta(milliseconds=1),
    )
    del output


def compile_cancellation_termination_and_failure(client: Client) -> None:
    client.stop_flow("cancel")
    client.stop_flow(
        "terminate",
        StopFlowOptions(StopType.TERMINATE, "terminated"),
    )
    client.stop_flow(
        "fail",
        StopFlowOptions(StopType.FAIL, "failed by API"),
    )


def compile_worker_failure_modes(client: Client) -> None:
    client.start_flow(ForceFailFlow(), "force-fail", 0)
    client.start_flow(StateFailureFlow(), "state-failure", 0)
    client.start_flow(StateTimeoutFlow(), "state-timeout", 0)
    client.start_flow(EmptyDecisionFlow(), "empty-decision", 0)
