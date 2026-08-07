# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from __future__ import annotations

from datetime import timedelta
from typing import Callable

import pytest
from dex import Client, IdReusePolicy, StartFlowOptions, StepExecutionId

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.patterns.workflow.parallel.job_seeker import JobSeeker
from dex_examples.patterns.workflow.recovery.failure_recovery_workflow_input import (
    FailureRecoveryWorkflowInput,
)
from dex_examples.patterns.workflow.storage.add_storage_item_request import (
    AddStorageItemRequest,
)
from dex_examples.patterns.workflow.storage.storage_flow import STORAGE_FLOW_ID
from dex_examples.patterns.workflow.waitforstatecompletion.job_seeker_data import (
    JobSeekerData,
)
from tests.integ.conftest import (
    LONG_WAIT_TIMEOUT,
    WAIT_TIMEOUT,
    flow_status_or_none,
    wait_until,
)

pytestmark = pytest.mark.integ


def test_cron_schedule_starts(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("cron")
    # Cron schedules may return an empty run ID; success is that start does not raise.
    client.start_flow(
        app.cron_schedule,
        flow_id,
        None,
        StartFlowOptions(timeout=timedelta(hours=1), cron_schedule="*/1 * * * *"),
    )


def test_drain_internal_channels(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("drain-internal")
    client.start_flow(
        app.drain_internal,
        flow_id,
        "documentId-0",
        start_options(),
    )
    client.wait_for_flow(flow_id, timeout=WAIT_TIMEOUT)


def test_drain_signal_channels(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("drain-signal")
    run_id = client.start_flow(
        app.drain_signal,
        flow_id,
        "first message from start",
        start_options(),
    )
    assert run_id
    client.publish(
        flow_id,
        app.drain_signal.queue_signal_channel,
        "signal from test",
    )


def test_interruptible_start_and_cancel(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("interruptible")
    client.start_flow(app.interruptible, flow_id, None, start_options())
    client.invoke_rpc(app.interruptible.interrupt, flow_id)
    client.wait_for_flow(flow_id, timeout=WAIT_TIMEOUT)


def test_manual_intervention_completes(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("intervention")
    client.start_flow(app.manual_intervention, flow_id, None, start_options())
    client.publish(flow_id, app.manual_intervention.data_channel, "success")
    output = client.wait_for_flow(flow_id, str, WAIT_TIMEOUT)
    assert "Workflow Completed" in output


def test_parallel_simple_and_with_await(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    simple_id = new_flow_id("parallel-simple")
    client.start_flow(
        app.simple_parallel,
        simple_id,
        JobSeeker("123", "jobseeker@example.com", "0987654321"),
        start_options(),
    )
    client.wait_for_flow(simple_id, timeout=WAIT_TIMEOUT)

    await_id = new_flow_id("parallel-await")
    client.start_flow(app.parallel_with_await, await_id, 5, start_options())
    client.wait_for_flow(await_id, timeout=WAIT_TIMEOUT)


def test_pattern_polling_simple_and_backoff(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    simple_id = new_flow_id("pattern-poll-simple")
    client.start_flow(app.simple_polling, simple_id, None, start_options())
    client.wait_for_flow(simple_id, timeout=WAIT_TIMEOUT)

    backoff_id = new_flow_id("pattern-poll-backoff")
    client.start_flow(app.backoff_polling, backoff_id, None, start_options())
    client.wait_for_flow(backoff_id, timeout=WAIT_TIMEOUT)


def test_failure_recovery(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    from dex import FlowStatus, FlowUncompletedError

    flow_id = new_flow_id("recovery")
    client.start_flow(
        app.failure_recovery,
        flow_id,
        FailureRecoveryWorkflowInput("widget", 2),
        start_options(),
    )
    with pytest.raises(FlowUncompletedError) as captured:
        client.wait_for_flow(flow_id, timeout=WAIT_TIMEOUT)
    assert captured.value.status is FlowStatus.FAILED
    assert "Failed to process transaction" in str(captured.value)


def test_reminder_accept(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("reminder")
    client.start_flow(app.reminder, flow_id, None, start_options())
    client.invoke_rpc(app.reminder.accept, flow_id)
    client.wait_for_flow(flow_id, timeout=WAIT_TIMEOUT)


def test_resettable_timer(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("resettable-timer")
    client.start_flow(app.resettable_timer, flow_id, None, start_options())
    client.invoke_rpc(app.resettable_timer.send_reset_message, flow_id)


def test_scalable_parallel(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("scalable-parallel")
    client.start_flow(
        app.request_receiver,
        flow_id,
        2,
        StartFlowOptions(
            timeout=timedelta(hours=1),
            id_reuse_policy=IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED,
        ),
    )
    client.wait_for_flow(flow_id, timeout=LONG_WAIT_TIMEOUT)


def test_parent_child(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("parent-child")
    # ParentFlowV2 keeps CONCURRENCY_PER_PARENT_WORKFLOW loops waiting on the
    # task queue, so it does not complete. Verify start + first child instead.
    run_id = client.start_flow(
        app.parent_flow_v2,
        flow_id,
        2,
        StartFlowOptions(
            timeout=timedelta(hours=1),
            id_reuse_policy=IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED,
        ),
    )
    assert run_id
    wait_until(
        "parent-child started a child flow",
        lambda: flow_status_or_none(client, "child-wf-0") is not None,
        WAIT_TIMEOUT,
    )


def test_storage_add_get_remove(
    app: ExampleApp,
    client: Client,
) -> None:
    from dex import DexException, ErrorSubStatus

    try:
        client.start_flow(
            app.storage,
            STORAGE_FLOW_ID,
            None,
            start_options(),
        )
    except DexException as error:
        if error.sub_status is not ErrorSubStatus.FLOW_ALREADY_STARTED:
            raise
    key = f"item-{id(app)}"
    client.invoke_rpc(
        app.storage.add_item,
        STORAGE_FLOW_ID,
        AddStorageItemRequest(key, "value-1"),
    )
    assert client.invoke_rpc(app.storage.get_item, STORAGE_FLOW_ID, key) == "value-1"
    client.invoke_rpc(app.storage.remove_item, STORAGE_FLOW_ID, key)


def test_wait_for_state_completion(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("wait-state")
    client.start_flow(
        app.wait_for_state_completion,
        flow_id,
        JobSeekerData(1),
        start_options(),
    )
    client.wait_for_step_completion(
        flow_id,
        StepExecutionId("PersistData"),
        timedelta(minutes=1),
    )
    data = client.invoke_rpc(app.wait_for_state_completion.get_job_seeker_data, flow_id)
    assert data.id == 1


def test_graceful_timeout_success(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("timeout-ok")
    client.start_flow(app.timeout, flow_id, True, start_options())
    assert (
        client.wait_for_flow(flow_id, str, WAIT_TIMEOUT)
        == "Workflow completed successfully"
    )
