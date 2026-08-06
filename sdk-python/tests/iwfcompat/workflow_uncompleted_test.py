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

from . import iwf_flows


def compile_wait_and_flow_timeouts(client: Client) -> None:
    options = StartFlowOptions(timeout=timedelta(seconds=1))
    client.start_flow(iwf_flows.SIGNAL, "wait-timeout", 0, options)
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
    client.start_flow(iwf_flows.FORCE_FAIL, "force-fail", 0)
    client.start_flow(iwf_flows.STATE_FAILURE, "state-failure", 0)
    client.start_flow(iwf_flows.STATE_TIMEOUT, "state-timeout", 0)
    client.start_flow(iwf_flows.EMPTY_DECISION, "empty-decision", 0)
