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
from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.products.shortlist_candidates import workflow_ids
from dex_examples.products.shortlist_candidates.employer_opt_in_input import (
    EmployerOptInInput,
)
from dex_examples.products.shortlist_candidates.shortlist_input import ShortlistInput
from dex_examples.products.signup.signup_form import SignupForm
from tests.integ.conftest import WAIT_TIMEOUT

from dex import AsyncClient, StartFlowOptions

pytestmark = pytest.mark.integ


async def test_user_onboarding_completes_all_tasks(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("signup")
    form = SignupForm(flow_id, f"{flow_id}@example.com", "Test", "User")
    await client.start_flow(app.user_onboarding, flow_id, form, start_options())
    await client.wait_for_attribute_equal(
        flow_id,
        app.user_onboarding.status,
        "waiting_for_verification",
        WAIT_TIMEOUT,
    )
    assert await client.invoke_rpc(app.user_onboarding.verify, flow_id) == "verified"
    await client.wait_for_attribute_equal(
        flow_id,
        app.user_onboarding.status,
        "waiting_for_task_1",
        WAIT_TIMEOUT,
    )
    assert (
        await client.invoke_rpc(app.user_onboarding.accomplish_task_1, flow_id)
        == "task 1 accomplished"
    )
    await client.wait_for_attribute_equal(
        flow_id,
        app.user_onboarding.status,
        "waiting_for_task_2",
        WAIT_TIMEOUT,
    )
    assert (
        await client.invoke_rpc(app.user_onboarding.accomplish_task_2, flow_id)
        == "task 2 accomplished"
    )
    assert (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        str
    ) == "onboarding completed"


async def test_job_post_create_and_read(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("jobpost")
    options = (
        StartFlowOptions(timeout=timedelta(hours=24))
        .with_attribute(app.job_post.title, "Software Engineer")
        .with_attribute(app.job_post.job_description, "Build durable workflows")
        .with_attribute(app.job_post.last_update_time_millis, 1)
        .with_attribute(app.job_post.notes, "initial")
    )
    await client.start_flow(app.job_post, flow_id, None, options)
    info = await client.invoke_rpc(app.job_post.get, flow_id)
    assert info.title == "Software Engineer"
    assert info.description == "Build durable workflows"
    assert info.notes == "initial"


async def test_shortlist_opt_in_and_revoke(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    employer_id = new_flow_id("employer")
    candidate_id = new_flow_id("candidate")
    opt_in_id = workflow_ids.employer_opt_in(employer_id)
    shortlist_id = workflow_ids.shortlist(employer_id, candidate_id)

    await client.start_flow(
        app.employer_opt_in,
        opt_in_id,
        EmployerOptInInput(employer_id),
        start_options(),
    )
    assert await client.invoke_rpc(app.employer_opt_in.is_opted_in, opt_in_id) is True

    await client.start_flow(
        app.shortlist,
        shortlist_id,
        ShortlistInput(employer_id, candidate_id),
        start_options(),
    )
    await client.publish(shortlist_id, app.shortlist.revoke_shortlist, None)
    await client.wait_for_flow(shortlist_id, timeout=WAIT_TIMEOUT)
