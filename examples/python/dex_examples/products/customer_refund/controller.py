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

"""HTTP routes that start, decide, and describe a customer refund on the shared example server."""

from __future__ import annotations

from dex import (
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    RpcLockConflictError,
    StepExecutionId,
    WaitForStepCompletionOptions,
    WorkerInvocationError,
)
from quart import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.products.customer_refund.refund_flow import (
    CASE_STATUS,
    REFUND_ID,
    DecisionReason,
)
from dex_examples.products.customer_refund.start import SCENARIOS
from dex_examples.shared.query import (
    new_flow_id,
    optional_query,
    required_query,
    started_flow,
)

CHECK_POLICY_STEP = StepExecutionId("CheckPolicy")


def create_customer_refund_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "customer_refund",
        __name__,
        url_prefix="/products/customer-refund/manager-approval",
    )

    @blueprint.errorhandler(FlowNotFoundError)
    async def report_missing_refund(error: FlowNotFoundError) -> Response:
        return _error_response(f"no refund Flow: {error.detail}", 404)

    @blueprint.errorhandler(FlowNotActiveOrNotFoundError)
    async def report_closed_refund(error: FlowNotActiveOrNotFoundError) -> Response:
        return _error_response("this refund is closed and no longer waits for a manager", 409)

    @blueprint.errorhandler(RpcLockConflictError)
    async def report_busy_refund(error: RpcLockConflictError) -> Response:
        return _error_response("another decision is changing this refund; try again", 409)

    @blueprint.get("/start")
    async def start() -> Response:
        scenario = optional_query("scenario", "") or "normal"
        if scenario not in SCENARIOS:
            choices = ", ".join(SCENARIOS)
            return _error_response(f"scenario must be one of {choices}", 400)
        flow_id = _optional_flow_id() or new_flow_id("customer-refund")
        run_id = await app_state.client.start_flow(
            app_state.customer_refund,
            flow_id,
            SCENARIOS[scenario],
            start_options(),
        )
        # After CheckPolicy, case-status tells whether the refund waits for a manager.
        await app_state.client.wait_for_step_completion(
            flow_id,
            CHECK_POLICY_STEP,
            WaitForStepCompletionOptions(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/decide")
    async def decide() -> Response:
        flow_id = _required_flow_id()
        decision = optional_query("decision", "")
        if decision not in ("approve", "reject"):
            return _error_response("decision must be approve or reject", 400)
        reason = optional_query("reason", "")
        if not reason.strip():
            return _error_response("a reason is required", 400)
        flow = app_state.customer_refund
        decide_rpc = flow.approve_refund if decision == "approve" else flow.reject_refund
        try:
            await app_state.client.invoke_rpc(decide_rpc, flow_id, DecisionReason(reason))
        except WorkerInvocationError as error:
            # The RPC refuses a run that is not waiting for a manager.
            return _error_response(error.worker_error_detail or error.detail, 409)
        return jsonify({"flowID": flow_id, "decision": decision})

    @blueprint.get("/describe")
    async def describe() -> Response:
        flow_id = _required_flow_id()
        description = await app_state.client.describe_flow(flow_id)
        values = await app_state.client.get_attributes(flow_id, CASE_STATUS, REFUND_ID)
        return jsonify(
            {
                "flowID": flow_id,
                "status": description.status.value,
                "caseStatus": values.get(CASE_STATUS, ""),
                "refundId": values.get(REFUND_ID, ""),
            }
        )

    return blueprint


def _optional_flow_id() -> str:
    """Reads the Flow ID from workflowId, or from its alias flowId; empty when absent."""
    return optional_query("workflowId", "") or optional_query("flowId", "")


def _required_flow_id() -> str:
    """Reads the Flow ID from flowId, or from its alias workflowId."""
    return optional_query("workflowId", "") or required_query("flowId")


def _error_response(message: str, status_code: int) -> Response:
    response = jsonify({"error": message})
    response.status_code = status_code
    return response
