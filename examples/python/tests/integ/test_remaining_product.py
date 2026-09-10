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
from dex_examples.products.job_post.job_info import JobInfo
from dex_examples.products.signup.signup_form import SignupForm
from tests.integ.conftest import WAIT_TIMEOUT

from dex import AsyncClient, StartFlowOptions, StepExecutionId

pytestmark = pytest.mark.integ

SECOND_LINKEDIN_POSTING_UPDATE = StepExecutionId("UpdateLinkedInPosting", 2)
SECOND_INDEED_POSTING_UPDATE = StepExecutionId("UpdateIndeedPosting", 2)
JOB_POSTING_INIT = StepExecutionId("InitStep", 1)


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


async def test_job_posting_create_read_and_update_both_job_boards(
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
        .with_attribute(app.job_post.update_version, 0)
    )
    await client.start_flow(app.job_post, flow_id, None, options)
    await client.wait_for_step_completion(flow_id, JOB_POSTING_INIT, WAIT_TIMEOUT)
    info = await client.invoke_rpc(app.job_post.get, flow_id)
    assert info.title == "Software Engineer"
    assert info.description == "Build durable workflows"
    assert info.notes == "initial"

    version = await client.invoke_rpc(
        app.job_post.update,
        flow_id,
        JobInfo("Senior Software Engineer", "Build durable systems", "updated"),
    )
    assert version == 1
    newest = JobInfo(
        "Principal Software Engineer",
        "Lead durable systems",
        "final scope",
    )
    version = await client.invoke_rpc(app.job_post.update, flow_id, newest)
    assert version == 2
    await client.wait_for_step_completion(
        flow_id,
        SECOND_LINKEDIN_POSTING_UPDATE,
        WAIT_TIMEOUT,
    )
    await client.wait_for_step_completion(
        flow_id,
        SECOND_INDEED_POSTING_UPDATE,
        WAIT_TIMEOUT,
    )
    updated = await client.invoke_rpc(app.job_post.get, flow_id)
    assert updated == newest
