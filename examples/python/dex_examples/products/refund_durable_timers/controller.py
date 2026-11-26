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

"""HTTP routes that drive the durable-Timer refund on the shared example server."""

from __future__ import annotations

from dex import (
    FlowAlreadyStartedError,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    RpcLockConflictError,
    WorkerInvocationError,
)
from quart import Blueprint, Response, current_app, jsonify

from dex_examples.app import ExampleApp
from dex_examples.products.refund_durable_timers.refund_timers_flow import (
    CASE_STATUS,
    REFUND_ID,
    REMINDERS_SENT,
    CustomerMessage,
    DecisionReason,
    RefundSimulation,
    SettlementPolling,
    refund_start_options,
)
from dex_examples.products.refund_durable_timers.skip_timer import (
    REFUND_TIMERS,
    TimerNotWaitingError,
    skip_refund_timer,
)
from dex_examples.products.refund_durable_timers.start import new_refund_request
from dex_examples.shared.query import new_flow_id, optional_query, started_flow


def create_refund_durable_timers_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "refund_durable_timers",
        __name__,
        url_prefix="/products/refund-durable-timers",
    )

    @blueprint.errorhandler(FlowNotFoundError)
    async def report_missing_refund(error: FlowNotFoundError) -> Response:
        return _error_response(f"no refund Flow: {error.detail}", 404)

    @blueprint.errorhandler(FlowNotActiveOrNotFoundError)
    async def report_closed_refund(error: FlowNotActiveOrNotFoundError) -> Response:
        return _error_response("this refund is closed or missing", 404)

    @blueprint.errorhandler(FlowAlreadyStartedError)
    async def report_duplicate_refund(error: FlowAlreadyStartedError) -> Response:
        return _error_response(error.detail, 409)

    @blueprint.errorhandler(RpcLockConflictError)
    async def report_busy_refund(error: RpcLockConflictError) -> Response:
        return _error_response("the refund is changing stage; try again", 409)

    @blueprint.errorhandler(WorkerInvocationError)
    async def report_refused_rpc(error: WorkerInvocationError) -> Response:
        return _error_response(error.worker_error_detail, 409)

    @blueprint.errorhandler(TimerNotWaitingError)
    async def report_timer_not_waiting(error: TimerNotWaitingError) -> Response:
        return _error_response(str(error), 409)

    @blueprint.errorhandler(ValueError)
    async def report_invalid_query(error: ValueError) -> Response:
        return _error_response(str(error), 400)

    @blueprint.errorhandler(Exception)
    async def report_route_failure(error: Exception) -> Response:
        current_app.logger.error("Refund durable timers route failed", exc_info=error)
        return _error_response("internal server error", 500)

    @blueprint.get("/start")
    async def start() -> Response:
        simulation = RefundSimulation(
            transient_payment_failures=int(optional_query("transientFailures", "0")),
            rate_limit_retry_after_seconds=int(optional_query("rateLimitRetryAfter", "0")),
            settle_after_checks=int(optional_query("settleAfterChecks", "1")),
            bank_never_settles=optional_query("neverSettle", "false").lower() == "true",
            heartbeat_check_interval_seconds=int(optional_query("heartbeatCheckInterval", "0")),
        )
        polling = SettlementPolling(optional_query("settlementPolling", "timer"))
        flow_id = _optional_flow_id() or new_flow_id("refund-timers")
        run_id = await app_state.client.start_flow(
            app_state.refund_durable_timers,
            flow_id,
            new_refund_request(polling, simulation),
            refund_start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/send-customer-message")
    async def send_customer_message() -> Response:
        flow_id = _required_flow_id()
        message = CustomerMessage(
            text=optional_query("text", "Here is my bank statement."),
            documents_complete=optional_query("documentsComplete", "false").lower() == "true",
        )
        flow = app_state.refund_durable_timers
        await app_state.client.invoke_rpc(flow.send_customer_message, flow_id, message)
        return jsonify({"flowID": flow_id, "documentsComplete": message.documents_complete})

    @blueprint.get("/decide")
    async def decide() -> Response:
        flow_id = _required_flow_id()
        decision = optional_query("decision", "")
        if decision not in ("approve", "reject"):
            raise ValueError("decision must be approve or reject")
        flow = app_state.refund_durable_timers
        decide_rpc = flow.approve_refund if decision == "approve" else flow.reject_refund
        reason = DecisionReason(optional_query("reason", ""))
        await app_state.client.invoke_rpc(decide_rpc, flow_id, reason)
        return jsonify({"flowID": flow_id, "decision": decision})

    @blueprint.get("/skip-timer")
    async def skip_timer() -> Response:
        flow_id = _required_flow_id()
        timer_name = optional_query("timer", "")
        if timer_name not in REFUND_TIMERS:
            raise ValueError(f"timer must be one of {', '.join(REFUND_TIMERS)}")
        execution = await skip_refund_timer(app_state.client, flow_id, timer_name)
        return jsonify(
            {
                "flowID": flow_id,
                "timer": timer_name,
                "stepType": execution.step_type,
                "execution": execution.number,
            }
        )

    @blueprint.get("/describe")
    async def describe() -> Response:
        flow_id = _required_flow_id()
        values = await app_state.client.get_attributes(
            flow_id, CASE_STATUS, REMINDERS_SENT, REFUND_ID
        )
        return jsonify(
            {
                "flowID": flow_id,
                "caseStatus": values.get(CASE_STATUS),
                "remindersSent": values.get(REMINDERS_SENT) or 0,
                "refundId": values.get(REFUND_ID),
            }
        )

    return blueprint


def _optional_flow_id() -> str:
    return optional_query("workflowId", "") or optional_query("flowId", "")


def _required_flow_id() -> str:
    flow_id = _optional_flow_id()
    if not flow_id:
        raise ValueError("workflowId is required")
    return flow_id


def _error_response(message: str, status_code: int) -> Response:
    response = jsonify({"error": message})
    response.status_code = status_code
    return response
