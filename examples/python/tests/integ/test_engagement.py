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

from typing import Callable

import pytest
from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.products.engagement.engagement_flow import EngagementFlow
from dex_examples.products.engagement.engagement_input import EngagementInput
from dex_examples.products.engagement.status import Status
from tests.integ.conftest import WAIT_TIMEOUT, wait_for_attribute, wait_until

from dex import AsyncClient

pytestmark = pytest.mark.integ


async def test_engagement_accept_completes_the_flow(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("engagement")
    await client.start_flow(
        app.engagement,
        flow_id,
        EngagementInput("test-employer-id", "test-job-seeker-id", "test-notes"),
        start_options(),
    )

    await wait_for_attribute(client, flow_id, EngagementFlow.engagement_status)
    description = await client.invoke_rpc(app.engagement.describe, flow_id)
    assert description.employer_id == "test-employer-id"
    assert description.job_seeker_id == "test-job-seeker-id"
    assert description.current_status == Status.INITIATED

    assert await client.invoke_rpc(app.engagement.accept, flow_id, "sounds good") is (
        Status.ACCEPTED
    )
    assert (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        str
    ) == "done"


async def test_engagement_decline_then_accept(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("engagement")
    await client.start_flow(
        app.engagement,
        flow_id,
        EngagementInput("test-employer-id", "test-job-seeker-id", None),
        start_options(),
    )

    await wait_for_attribute(client, flow_id, EngagementFlow.engagement_status)
    assert await client.invoke_rpc(app.engagement.decline, flow_id, "not now") is (
        Status.DECLINED
    )

    async def engagement_is_declined() -> bool:
        return (
            await client.get_attribute(flow_id, EngagementFlow.engagement_status)
            is Status.DECLINED
        )

    await wait_until("the engagement to become declined", engagement_is_declined)
    assert await client.invoke_rpc(
        app.engagement.accept, flow_id, "changed my mind"
    ) is (Status.ACCEPTED)

    assert (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        str
    ) == "done"
    description = await client.invoke_rpc(app.engagement.describe, flow_id)
    assert description.notes == ";not now;changed my mind"


async def test_engagement_opt_out_of_reminders(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("engagement")
    await client.start_flow(
        app.engagement,
        flow_id,
        EngagementInput("test-employer-id", "test-job-seeker-id", "test-notes"),
        start_options(),
    )

    await wait_for_attribute(client, flow_id, EngagementFlow.engagement_status)
    await client.publish(flow_id, app.engagement.opt_out_reminder, None)

    # Opting out ends the reminder loop but leaves the engagement open.
    async def reminder_is_stopped() -> bool:
        description = await client.invoke_rpc(app.engagement.describe, flow_id)
        return "user opted out of reminders" in description.notes

    await wait_until("the reminder loop to stop", reminder_is_stopped)
    await client.invoke_rpc(app.engagement.accept, flow_id, "")
    assert (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        str
    ) == "done"
