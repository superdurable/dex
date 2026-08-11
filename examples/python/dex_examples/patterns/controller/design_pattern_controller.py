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
import time
from dataclasses import asdict
from datetime import timedelta

from dex import (
    FlowConfig,
    FlowNotActiveError,
    IdReusePolicy,
    StartFlowOptions,
    StepExecutionId,
)
from quart import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.controller.query import (
    optional_query,
    required_body_field,
    required_bool_body_field,
    required_int_query,
    required_query,
)
from dex_examples.patterns.workflow.entitystore.user_profile import UserProfileRequest
from dex_examples.patterns.workflow.entitystore.user_profile_flow import STORE_NAME
from dex_examples.patterns.workflow.parallel.job_seeker import JobSeeker
from dex_examples.patterns.workflow.recovery.failure_recovery_workflow_input import (
    FailureRecoveryWorkflowInput,
)
from dex_examples.patterns.workflow.waitforstatecompletion.job_seeker_data import (
    JobSeekerData,
)

PERSIST_DATA_STEP = StepExecutionId("PersistData")
PERSIST_DATA_TIMEOUT = timedelta(minutes=5)
RETRY_PREVIOUS_FAILED = StartFlowOptions(
    timeout=timedelta(hours=1),
    id_reuse_policy=IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED,
)


def create_design_pattern_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("design_pattern", __name__, url_prefix="/design-pattern")

    @blueprint.get("/polling/start/simple")
    async def start_simple_polling() -> str:
        return await app_state.client.start_flow(
            app_state.simple_polling,
            required_query("workflowId"),
            None,
            start_options(),
        )

    @blueprint.get("/polling/start/backoff")
    async def start_backoff_polling() -> str:
        return await app_state.client.start_flow(
            app_state.backoff_polling,
            required_query("workflowId"),
            None,
            start_options(),
        )

    @blueprint.get("/interruptible/start")
    async def start_interruptible() -> str:
        return await app_state.client.start_flow(
            app_state.interruptible,
            required_query("workflowId"),
            None,
            start_options(),
        )

    @blueprint.get("/interruptible/cancel")
    async def cancel_interruptible() -> str:
        await app_state.client.invoke_rpc(
            app_state.interruptible.interrupt,
            required_query("workflowId"),
        )
        return "done"

    @blueprint.get("/workflow-with-reminder/start")
    async def start_reminder() -> str:
        flow_id = f"reminder_test_id_{time.time_ns()}"
        await app_state.client.start_flow(
            app_state.reminder,
            flow_id,
            None,
            start_options(),
        )
        return f"started workflowId: {flow_id}"

    @blueprint.get("/workflow-with-reminder/accept")
    async def accept_reminder() -> str:
        await app_state.client.invoke_rpc(
            app_state.reminder.accept,
            required_query("workflowId"),
        )
        return "accepted"

    @blueprint.get("/workflow-with-reminder/optout")
    async def opt_out_reminder() -> str:
        await app_state.client.publish(
            required_query("workflowId"),
            app_state.reminder.opt_out_reminder,
            None,
        )
        return "done"

    @blueprint.post("/entity-store/profile")
    async def create_user_profile() -> str:
        profile_request = UserProfileRequest(
            await required_body_field("userId"),
            await required_body_field("displayName"),
            await required_body_field("email"),
            await required_bool_body_field("marketingOptIn"),
        )
        profile = profile_request.profile()
        options = (
            StartFlowOptions(
                timeout=timedelta(hours=1),
                config_override=FlowConfig(attribute_store_name=STORE_NAME),
            )
            .with_attribute(app_state.user_profile.display_name, profile.display_name)
            .with_attribute(app_state.user_profile.email, profile.email)
            .with_attribute(
                app_state.user_profile.marketing_opt_in,
                profile.marketing_opt_in,
            )
        )
        return await app_state.client.start_flow(
            app_state.user_profile,
            profile_request.user_id,
            None,
            options,
        )

    @blueprint.post("/entity-store/profile/update")
    async def update_user_profile() -> str:
        profile_request = UserProfileRequest(
            await required_body_field("userId"),
            await required_body_field("displayName"),
            await required_body_field("email"),
            await required_bool_body_field("marketingOptIn"),
        )
        await app_state.client.invoke_rpc(
            app_state.user_profile.update_profile,
            profile_request.user_id,
            profile_request.profile(),
        )
        return "Updated user profile"

    @blueprint.get("/entity-store/profile")
    async def get_user_profile() -> Response:
        profile = await app_state.client.invoke_rpc(
            app_state.user_profile.get_profile,
            required_query("userId"),
        )
        return jsonify(asdict(profile))

    @blueprint.post("/entity-store/profile/clear")
    async def clear_user_profile() -> str:
        await app_state.client.invoke_rpc(
            app_state.user_profile.clear_profile,
            required_query("userId"),
        )
        return "Cleared user profile"

    @blueprint.get("/intervention/start")
    async def start_intervention() -> str:
        return await app_state.client.start_flow(
            app_state.manual_intervention,
            required_query("workflowId"),
            None,
            start_options(),
        )

    @blueprint.get("/resettabletimer/start")
    async def start_resettable_timer() -> str:
        return await app_state.client.start_flow(
            app_state.resettable_timer,
            required_query("workflowId"),
            None,
            start_options(),
        )

    @blueprint.get("/resettabletimer/reset")
    async def reset_resettable_timer() -> str:
        await app_state.client.invoke_rpc(
            app_state.resettable_timer.send_reset_message,
            required_query("workflowId"),
        )
        return "reset"

    @blueprint.get("/parallel/start/simple")
    async def start_simple_parallel() -> str:
        return await app_state.client.start_flow(
            app_state.simple_parallel,
            required_query("workflowId"),
            JobSeeker("123", "jobseeker@indeed.com", "0987654321"),
            start_options(),
        )

    @blueprint.get("/parallel/start/withAwait")
    async def start_parallel_with_await() -> str:
        return await app_state.client.start_flow(
            app_state.parallel_with_await,
            required_query("workflowId"),
            50,
            start_options(),
        )

    @blueprint.get("/recovery/start")
    async def start_recovery() -> str:
        await app_state.client.start_flow(
            app_state.failure_recovery,
            required_query("workflowId"),
            FailureRecoveryWorkflowInput(
                required_query("itemName"),
                required_int_query("quantity"),
            ),
            start_options(),
        )
        return "recovery workflow started"

    @blueprint.get("/scalableparallel/start")
    async def start_scalable_parallel() -> str:
        await app_state.client.start_flow(
            app_state.request_receiver,
            required_query("workflowId"),
            required_int_query("numOfChildWfs"),
            RETRY_PREVIOUS_FAILED,
        )
        return "success"

    @blueprint.get("/parentchild/start")
    async def start_parent_child() -> str:
        await app_state.client.start_flow(
            app_state.parent_flow_v2,
            required_query("workflowId"),
            required_int_query("numOfChildWfs"),
            RETRY_PREVIOUS_FAILED,
        )
        return "success"

    @blueprint.get("/drainchannels/internal/start")
    async def start_drain_internal_channels() -> str:
        return await app_state.client.start_flow(
            app_state.drain_internal,
            required_query("workflowId"),
            "start-input",
            start_options(),
        )

    @blueprint.get("/drainchannels/signal/startorsignal")
    async def start_or_signal_drain_signal_channels() -> str:
        flow_id = required_query("workflowId")
        try:
            await app_state.client.publish(
                flow_id,
                app_state.drain_signal.queue_signal_channel,
                "signal from startorsignal endpoint",
            )
        except FlowNotActiveError:
            run_id = await app_state.client.start_flow(
                app_state.drain_signal,
                flow_id,
                "first message from start",
                start_options(),
            )
            return f"Started the workflow with runId {run_id}"
        return "Signaled the workflow"

    @blueprint.get("/waitforstatecompletion/start")
    async def start_wait_for_state_completion() -> str:
        flow_id = required_query("workflowId")
        await app_state.client.start_flow(
            app_state.wait_for_state_completion,
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
            app_state.wait_for_state_completion.get_job_seeker_data,
            flow_id,
        )
        payload = json.dumps(asdict(persisted), sort_keys=True)
        return f"success for workflow {flow_id} with data {payload}"

    @blueprint.get("/timeout/start")
    async def start_timeout() -> str:
        flow_id = required_query("workflowId")
        await app_state.client.start_flow(
            app_state.timeout,
            flow_id,
            optional_query("successfulWorkflow", "true").lower() == "true",
            start_options(),
        )
        return f"success for workflow {flow_id}"

    return blueprint
