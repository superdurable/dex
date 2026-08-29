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

import json
from dataclasses import asdict
from datetime import timedelta

from dex import StepExecutionId
from quart import Blueprint

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.patterns.wait_for_step_completion.job_seeker_data import JobSeekerData
from dex_examples.shared.query import required_query

PERSIST_DATA_STEP = StepExecutionId("PersistData")
PERSIST_DATA_TIMEOUT = timedelta(minutes=5)


def create_wait_for_step_completion_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_wait_for_step_completion",
        __name__,
        url_prefix="/patterns/wait-for-step-completion",
    )

    @blueprint.get("/start")
    async def start_wait_for_step_completion() -> str:
        flow_id = required_query("workflowId")
        await app_state.client.start_flow(
            app_state.wait_for_step_completion,
            flow_id,
            JobSeekerData(1),
            start_options(),
        )
        await app_state.client.wait_for_step_completion(
            flow_id,
            PERSIST_DATA_STEP,
            PERSIST_DATA_TIMEOUT,
        )
        persisted = await app_state.client.invoke_rpc(
            app_state.wait_for_step_completion.get_job_seeker_data,
            flow_id,
        )
        payload = json.dumps(asdict(persisted), sort_keys=True)
        return f"success for workflow {flow_id} with data {payload}"

    return blueprint
