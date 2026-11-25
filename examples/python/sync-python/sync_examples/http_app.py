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

from dataclasses import asdict
from datetime import timedelta

from dex import (
    FlowAlreadyStartedError,
    FlowClosingError,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    IdReusePolicy,
    RequestTimeoutError,
    RpcLockConflictError,
    StartFlowOptions,
    StepExecutionId,
    WaitForStepCompletionOptions,
    WorkerInvocationError,
)
from flask import Blueprint, Flask, Response, current_app, jsonify

from dex_examples.http_cors import install_flask_cors
from dex_examples.products.customer_refund.refund_flow import (
    CASE_STATUS,
    REFUND_ID,
    REFUND_NOT_AWAITING_MANAGER_ERROR_TYPE,
    DecisionReason,
    refund_run_status,
)
from dex_examples.products.customer_refund.start import SCENARIOS
from dex_examples.products.engagement.engagement_input import EngagementInput
from dex_examples.products.money_transfer.transfer_request import TransferRequest
from dex_examples.products.subscription.customer import Customer
from dex_examples.products.subscription.subscription import Subscription
from sync_examples.app import SyncExampleApp
from sync_examples.config import DEFAULT_TIMEOUT, start_options
from sync_examples.query import (
    new_flow_id,
    optional_query,
    required_int_query,
    required_query,
    started_flow,
)

CHECK_POLICY_TIMEOUT_SECONDS = 10


def create_app(app_state: SyncExampleApp) -> Flask:
    app = Flask(__name__)
    app.register_blueprint(_step(app_state))
    app.register_blueprint(_channel(app_state))
    app.register_blueprint(_money_transfer(app_state))
    app.register_blueprint(_customer_refund(app_state))
    app.register_blueprint(_engagement(app_state))
    app.register_blueprint(_subscription(app_state))
    app.register_blueprint(_parent_child(app_state))
    app.register_blueprint(_control_long_running_work(app_state))
    install_flask_cors(app)

    @app.get("/health")
    def health() -> Response:
        return jsonify({"ok": True})

    return app


def _step(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint("primitive_step", __name__, url_prefix="/primitives/step")

    @blueprint.get("/start")
    def start() -> Response:
        flow_id = required_query("workflowId")
        run_id = app_state.client.start_flow(
            app_state.step,
            flow_id,
            required_int_query("inputNum"),
            start_options(),
        )
        return started_flow(flow_id, run_id)

    return blueprint


def _channel(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint("primitive_channel", __name__, url_prefix="/primitives/channel")

    @blueprint.get("/start")
    def start() -> Response:
        flow_id = required_query("workflowId")
        run_id = app_state.client.start_flow(
            app_state.channel,
            flow_id,
            required_int_query("inputNum"),
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/approve")
    def approve() -> str:
        app_state.client.invoke_rpc(
            app_state.channel.publish_approval_message,
            required_query("workflowId"),
        )
        return "done"

    return blueprint


def _money_transfer(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint("money_transfer", __name__, url_prefix="/products/money-transfer")

    @blueprint.get("/start")
    def start() -> Response:
        request = TransferRequest(
            required_query("fromAccount"),
            required_query("toAccount"),
            required_int_query("amount"),
            optional_query("notes", ""),
        )
        flow_id = new_flow_id("money-transfer")
        run_id = app_state.client.start_flow(
            app_state.money_transfer,
            flow_id,
            request,
            start_options(),
        )
        return started_flow(flow_id, run_id)

    return blueprint


def _customer_refund(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "customer_refund",
        __name__,
        url_prefix="/products/customer-refund/manager-approval",
    )

    @blueprint.errorhandler(FlowNotFoundError)
    def report_missing_refund(error: FlowNotFoundError) -> tuple[Response, int]:
        return jsonify({"error": f"no refund Flow: {error.detail}"}), 404

    @blueprint.errorhandler(FlowNotActiveOrNotFoundError)
    def report_closed_refund(error: FlowNotActiveOrNotFoundError) -> tuple[Response, int]:
        return jsonify({"error": "this refund is closed or missing"}), 404

    @blueprint.errorhandler(FlowAlreadyStartedError)
    def report_duplicate_refund(error: FlowAlreadyStartedError) -> tuple[Response, int]:
        return jsonify({"error": error.detail}), 409

    @blueprint.errorhandler(FlowClosingError)
    def report_closing_refund(error: FlowClosingError) -> tuple[Response, int]:
        return jsonify({"error": "this refund is closing"}), 409

    @blueprint.errorhandler(RpcLockConflictError)
    def report_busy_refund(error: RpcLockConflictError) -> tuple[Response, int]:
        return jsonify({"error": "another decision is changing this refund; try again"}), 409

    @blueprint.errorhandler(WorkerInvocationError)
    def report_failed_refund_rpc(error: WorkerInvocationError) -> tuple[Response, int]:
        # The RPC refuses a run that is not waiting for a manager.
        if error.worker_error_type == REFUND_NOT_AWAITING_MANAGER_ERROR_TYPE:
            return jsonify({"error": error.worker_error_detail}), 409
        return report_refund_route_failure(error)

    @blueprint.errorhandler(Exception)
    def report_refund_route_failure(error: Exception) -> tuple[Response, int]:
        current_app.logger.error("Customer refund route failed", exc_info=error)
        return jsonify({"error": "internal server error"}), 500

    @blueprint.get("/start")
    def start() -> tuple[Response, int]:
        scenario = optional_query("scenario", "normal")
        if scenario not in SCENARIOS:
            return jsonify({"error": f"scenario must be one of {', '.join(SCENARIOS)}"}), 400
        flow_id = _refund_flow_id() or new_flow_id("customer-refund")
        run_id = app_state.client.start_flow(
            app_state.customer_refund,
            flow_id,
            SCENARIOS[scenario],
            start_options(),
        )
        # After CheckPolicy, case-status tells whether the refund waits for a manager.
        try:
            app_state.client.wait_for_step_completion(
                flow_id,
                StepExecutionId("CheckPolicy"),
                WaitForStepCompletionOptions(
                    request_timeout=timedelta(seconds=CHECK_POLICY_TIMEOUT_SECONDS)
                ),
            )
        except RequestTimeoutError:
            return jsonify({
                "error": f"refund policy check did not finish within {CHECK_POLICY_TIMEOUT_SECONDS} seconds"
            }), 504
        except (FlowClosingError, FlowNotActiveOrNotFoundError):
            # Denied and small refunds can close before the wait attaches.
            pass
        # Same keys as the async app, so every language returns flowID and runID.
        return jsonify({"flowID": flow_id, "runID": run_id}), 200

    @blueprint.get("/decide")
    def decide() -> tuple[Response, int]:
        flow_id = _refund_flow_id()
        if not flow_id:
            return jsonify({"error": "workflowId is required"}), 400
        decision = optional_query("decision", "")
        if decision not in ("approve", "reject"):
            return jsonify({"error": "decision must be approve or reject"}), 400
        reason = optional_query("reason", "")
        if not reason.strip():
            return jsonify({"error": "a reason is required"}), 400
        flow = app_state.customer_refund
        description = app_state.client.describe_flow(flow_id)
        if description.flow_type != flow.get_flow_type():
            return jsonify({"error": "no refund Flow with this ID"}), 404
        decide_rpc = flow.approve_refund if decision == "approve" else flow.reject_refund
        app_state.client.invoke_rpc(
            decide_rpc, flow_id, DecisionReason(reason), run_id=description.run_id
        )
        return jsonify({"flowID": flow_id, "decision": decision}), 200

    @blueprint.get("/describe")
    def describe() -> tuple[Response, int]:
        flow_id = _refund_flow_id()
        if not flow_id:
            return jsonify({"error": "workflowId is required"}), 400
        description = app_state.client.describe_flow(flow_id)
        if description.flow_type != app_state.customer_refund.get_flow_type():
            return jsonify({"error": "no refund Flow with this ID"}), 404
        values = app_state.client.get_attributes(flow_id, CASE_STATUS, REFUND_ID)
        return jsonify(
            {
                "flowID": flow_id,
                "status": refund_run_status(description.status),
                "caseStatus": values.get(CASE_STATUS),
                "refundId": values.get(REFUND_ID),
            }
        ), 200

    return blueprint


def _refund_flow_id() -> str:
    """Reads the Flow ID from workflowId, or from its alias flowId; empty when absent."""
    return optional_query("workflowId", "") or optional_query("flowId", "")


def _engagement(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint("engagement", __name__, url_prefix="/products/engagement")

    @blueprint.get("/start")
    def start() -> Response:
        flow_id = new_flow_id("engagement")
        run_id = app_state.client.start_flow(
            app_state.engagement,
            flow_id,
            EngagementInput("test-employer-id", "test-job-seeker-id", "test-notes"),
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/describe")
    def describe() -> Response:
        description = app_state.client.invoke_rpc(
            app_state.engagement.describe,
            required_query("workflowId"),
        )
        return jsonify(asdict(description))

    @blueprint.get("/accept")
    def accept() -> Response:
        status = app_state.client.invoke_rpc(
            app_state.engagement.accept,
            required_query("workflowId"),
            optional_query("notes", ""),
        )
        return jsonify(status.value)

    @blueprint.get("/decline")
    def decline() -> Response:
        status = app_state.client.invoke_rpc(
            app_state.engagement.decline,
            required_query("workflowId"),
            optional_query("notes", ""),
        )
        return jsonify(status.value)

    return blueprint


def _subscription(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint("subscription", __name__, url_prefix="/products/subscription")

    @blueprint.get("/start")
    def start() -> Response:
        customer = Customer(
            "Quanzheng",
            "Long",
            "qlong",
            "qlong@example.com",
            Subscription(
                trial_period_seconds=20,
                billing_period_seconds=10,
                max_billing_periods=10,
                billing_period_charge=100,
            ),
        )
        flow_id = new_flow_id("subscription")
        run_id = app_state.client.start_flow(
            app_state.subscription,
            flow_id,
            customer,
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/cancel")
    def cancel() -> str:
        app_state.client.invoke_rpc(
            app_state.subscription.cancel_subscription_rpc,
            required_query("workflowId"),
        )
        return "accepted"

    @blueprint.get("/describe")
    def describe() -> Response:
        subscription = app_state.client.invoke_rpc(
            app_state.subscription.describe,
            required_query("workflowId"),
        )
        return jsonify(asdict(subscription))

    return blueprint


def _parent_child(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint("parent_child", __name__, url_prefix="/patterns/parent-child")

    @blueprint.get("/start")
    def parent_child_start() -> Response:
        flow_id = optional_query("workflowId", new_flow_id("parent-child"))
        num_children = required_int_query("numOfChildWfs")
        run_id = app_state.client.start_flow(
            app_state.parent_flow_v2,
            flow_id,
            num_children,
            StartFlowOptions(
                timeout=DEFAULT_TIMEOUT,
                id_reuse_policy=IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED,
            ),
        )
        return started_flow(flow_id, run_id)

    return blueprint


def _control_long_running_work(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "control_long_running_work",
        __name__,
        url_prefix="/patterns/control-long-running-work",
    )

    @blueprint.get("/start")
    def start_record_backfill() -> Response:
        flow_id = optional_query("workflowId", new_flow_id("control-long-running-work"))
        run_id = app_state.client.start_flow(
            app_state.record_backfill,
            flow_id,
            None,
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/set-batch-size")
    def set_batch_size() -> str:
        app_state.client.invoke_rpc(
            app_state.record_backfill.set_batch_size,
            required_query("workflowId"),
            required_int_query("batchSize"),
        )
        return "done"

    @blueprint.get("/stop")
    def request_stop() -> str:
        app_state.client.invoke_rpc(
            app_state.record_backfill.request_stop,
            required_query("workflowId"),
        )
        return "done"

    return blueprint
