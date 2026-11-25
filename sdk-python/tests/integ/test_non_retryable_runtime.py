# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta

from dex import Flow, FlowStatus

from .environment import DexDevTestEnvironment
from .non_retryable_flow import (
    NON_RETRYABLE_DETAIL,
    NonRetryableExecuteFlow,
    NonRetryableWaitForFlow,
    attempts_by_flow_id,
    recovery_by_flow_id,
)
from .shared import unique_id

# Far below the hourly retry interval: completing in time proves no retry ran.
WAIT_TIMEOUT = timedelta(seconds=30)


def _run_until_recovered(flow: Flow, flow_id: str, flow_input: object) -> None:
    with DexDevTestEnvironment(flow) as environment:
        environment.client.start_flow(flow, flow_id, flow_input)
        result = environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.status is FlowStatus.COMPLETED
    assert attempts_by_flow_id[flow_id] == 1
    recovery = recovery_by_flow_id[flow_id]
    assert recovery.detail == NON_RETRYABLE_DETAIL
    assert recovery.error_type.endswith("CardRejectedError")


def test_non_retryable_execute_failure_skips_retries_and_proceeds() -> None:
    _run_until_recovered(
        NonRetryableExecuteFlow(), unique_id("non-retryable-execute"), "order"
    )


def test_non_retryable_wait_for_failure_skips_retries_and_proceeds() -> None:
    _run_until_recovered(
        NonRetryableWaitForFlow(), unique_id("non-retryable-wait-for"), None
    )
