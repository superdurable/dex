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

from datetime import datetime, timedelta, timezone
from typing import Callable

import pytest
from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.patterns.entity_store.user_profile import (
    UserProfile,
    UserProfileMetadata,
)
from dex_examples.patterns.entity_store.user_profile_flow import STORE_NAME
from dex_examples.patterns.parallel.job_seeker import JobSeeker
from dex_examples.patterns.recovery.failure_recovery_workflow_input import (
    FailureRecoveryWorkflowInput,
)
from dex_examples.patterns.wait_for_state_completion.job_seeker_data import (
    JobSeekerData,
)
from tests.integ.conftest import (
    LONG_WAIT_TIMEOUT,
    WAIT_TIMEOUT,
    flow_status_or_none,
    wait_until,
)

from dex import (
    AsyncClient,
    FlowConfig,
    FlowStatus,
    IdReusePolicy,
    StartFlowOptions,
    StepExecutionId,
)

pytestmark = pytest.mark.integ


async def test_cron_schedule_starts(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("cron")
    # Cron schedules may return an empty run ID; success is that start does not raise.
    await client.start_flow(
        app.cron_schedule,
        flow_id,
        None,
        StartFlowOptions(timeout=timedelta(hours=1), cron_schedule="0 * * * *"),
    )


async def test_drain_internal_channels(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("drain-internal")
    await client.start_flow(
        app.drain_internal,
        flow_id,
        "documentId-0",
        start_options(),
    )
    await client.wait_for_flow(flow_id, WAIT_TIMEOUT)


async def test_drain_signal_channels(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("drain-signal")
    run_id = await client.start_flow(
        app.drain_signal,
        flow_id,
        "first message from start",
        start_options(),
    )
    assert run_id
    await client.publish(
        flow_id,
        app.drain_signal.queue_signal_channel,
        "signal from test",
    )


async def test_interruptible_start_and_cancel(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("interruptible")
    await client.start_flow(app.interruptible, flow_id, None, start_options())
    await wait_until(
        "interruptible flow running",
        lambda: _status_is(client, flow_id, FlowStatus.RUNNING),
    )
    await client.invoke_rpc(app.interruptible.interrupt, flow_id)
    await client.wait_for_flow(flow_id, WAIT_TIMEOUT)


async def _status_is(client: AsyncClient, flow_id: str, status: FlowStatus) -> bool:
    return await flow_status_or_none(client, flow_id) is status


async def test_manual_intervention_completes(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("intervention")
    await client.start_flow(app.manual_intervention, flow_id, None, start_options())
    await client.publish(flow_id, app.manual_intervention.data_channel, "success")
    output = (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(str)
    assert "Workflow Completed" in output


async def test_parallel_simple_and_with_await(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    simple_id = new_flow_id("parallel-simple")
    await client.start_flow(
        app.simple_parallel,
        simple_id,
        JobSeeker("123", "jobseeker@example.com", "0987654321"),
        start_options(),
    )
    await client.wait_for_flow(simple_id, WAIT_TIMEOUT)

    await_id = new_flow_id("parallel-await")
    await client.start_flow(app.parallel_with_await, await_id, 5, start_options())
    await client.wait_for_flow(await_id, WAIT_TIMEOUT)


async def test_pattern_polling_simple_and_backoff(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    simple_id = new_flow_id("pattern-poll-simple")
    await client.start_flow(app.simple_polling, simple_id, None, start_options())
    await client.wait_for_flow(simple_id, WAIT_TIMEOUT)

    backoff_id = new_flow_id("pattern-poll-backoff")
    await client.start_flow(app.backoff_polling, backoff_id, None, start_options())
    await client.wait_for_flow(backoff_id, WAIT_TIMEOUT)


async def test_failure_recovery(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("recovery")
    await client.start_flow(
        app.failure_recovery,
        flow_id,
        FailureRecoveryWorkflowInput("widget", 2),
        start_options(),
    )
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.FAILED
    assert "Failed to process transaction" in (result.error_message or "")


async def test_reminder_accept(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("reminder")
    await client.start_flow(app.reminder, flow_id, None, start_options())
    await client.invoke_rpc(app.reminder.accept, flow_id)
    await client.wait_for_flow(flow_id, WAIT_TIMEOUT)


async def test_resettable_timer(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("resettable-timer")
    await client.start_flow(app.resettable_timer, flow_id, None, start_options())
    await client.invoke_rpc(app.resettable_timer.send_reset_message, flow_id)


async def test_scalable_parallel(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("scalable-parallel")
    await client.start_flow(
        app.request_receiver,
        flow_id,
        2,
        StartFlowOptions(
            timeout=timedelta(hours=1),
            id_reuse_policy=IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED,
        ),
    )
    await client.wait_for_flow(flow_id, LONG_WAIT_TIMEOUT)


async def test_parent_child(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("parent-child")
    # ParentFlowV2 keeps CONCURRENCY_PER_PARENT_WORKFLOW loops waiting on the
    # task queue, so it does not complete. Verify start + first child instead.
    run_id = await client.start_flow(
        app.parent_flow_v2,
        flow_id,
        2,
        StartFlowOptions(
            timeout=timedelta(hours=1),
            id_reuse_policy=IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED,
        ),
    )
    assert run_id
    await wait_until(
        "parent-child started a child flow",
        lambda: flow_status_or_none(client, "child-wf-0"),
        WAIT_TIMEOUT,
    )


async def test_entity_store_profile_lifecycle(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("entity-store")
    profile = UserProfile(
        "Ada Lovelace",
        "ada@example.com",
        True,
        120,
        59.5,
        datetime(2026, 8, 11, 15, 30, tzinfo=timezone.utc),
        UserProfileMetadata("integration", ["example", "pro"]),
    )
    options = (
        StartFlowOptions(
            timeout=timedelta(hours=1),
            config_override=FlowConfig(attribute_store_name=STORE_NAME),
        )
        .with_attribute(app.user_profile.display_name, profile.display_name)
        .with_attribute(app.user_profile.email, profile.email)
        .with_attribute(app.user_profile.marketing_opt_in, profile.marketing_opt_in)
        .with_attribute(app.user_profile.credits, profile.credits)
        .with_attribute(app.user_profile.weight, profile.weight)
        .with_attribute(
            app.user_profile.last_logged_in_time, profile.last_logged_in_time
        )
        .with_attribute(app.user_profile.metadata, profile.metadata)
    )
    await client.start_flow(app.user_profile, flow_id, None, options)
    updated = UserProfile(
        "Ada Byron",
        "ada@example.com",
        False,
        180,
        60.25,
        datetime(2026, 8, 12, 9, 45, tzinfo=timezone.utc),
        UserProfileMetadata("integration", ["example", "enterprise"]),
    )
    await client.invoke_rpc(
        app.user_profile.update_profile,
        flow_id,
        updated,
    )
    assert await client.invoke_rpc(app.user_profile.get_profile, flow_id) == updated
    await client.invoke_rpc(app.user_profile.clear_profile, flow_id)


async def test_wait_for_state_completion(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("wait-state")
    await client.start_flow(
        app.wait_for_state_completion,
        flow_id,
        JobSeekerData(1),
        start_options(),
    )
    await client.wait_for_step_completion(
        flow_id,
        StepExecutionId("PersistData"),
        timedelta(minutes=1),
    )
    data = await client.invoke_rpc(
        app.wait_for_state_completion.get_job_seeker_data, flow_id
    )
    assert data.id == 1


async def test_graceful_timeout_success(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("timeout-ok")
    await client.start_flow(app.timeout, flow_id, True, start_options())
    assert (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        str
    ) == "Workflow completed successfully"
