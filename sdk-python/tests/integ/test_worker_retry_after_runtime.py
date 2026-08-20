# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

import time
from datetime import timedelta

from dex import FlowStatus

from .environment import DexDevTestEnvironment
from .flow_service_client import (
    assert_worker_failure_stack_trace,
    await_live_worker_failure,
)
from .shared import unique_id
from .worker_retry_after_flow import (
    EXECUTE_RETRY_AFTER_DETAIL,
    RETRY_AFTER_SECONDS,
    RETRY_POLICY_INTERVAL_SECONDS,
    WAIT_FOR_RETRY_AFTER_DETAIL,
    WorkerRetryAfterExecuteFlow,
    WorkerRetryAfterWaitForFlow,
)

WAIT_TIMEOUT = timedelta(seconds=30)

def test_wait_for_retry_after_stack_trace_and_delay() -> None:
    flow = WorkerRetryAfterWaitForFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("wait-retry-after")
        started_at = time.monotonic()
        run_id = environment.client.start_flow(flow, flow_id, None)
        failure = await_live_worker_failure(flow_id, run_id)
        assert_worker_failure_stack_trace(failure, WAIT_FOR_RETRY_AFTER_DETAIL)
        result = environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.status is FlowStatus.COMPLETED
        elapsed = time.monotonic() - started_at
        assert elapsed >= RETRY_AFTER_SECONDS
        assert elapsed < RETRY_POLICY_INTERVAL_SECONDS

def test_execute_retry_after_stack_trace_and_delay() -> None:
    flow = WorkerRetryAfterExecuteFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("execute-retry-after")
        started_at = time.monotonic()
        run_id = environment.client.start_flow(flow, flow_id, None)
        failure = await_live_worker_failure(flow_id, run_id)
        assert_worker_failure_stack_trace(failure, EXECUTE_RETRY_AFTER_DETAIL)
        result = environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.status is FlowStatus.COMPLETED
        elapsed = time.monotonic() - started_at
        assert elapsed >= RETRY_AFTER_SECONDS
        assert elapsed < RETRY_POLICY_INTERVAL_SECONDS
