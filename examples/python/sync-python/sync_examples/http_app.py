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

from dex import IdReusePolicy, StartFlowOptions
from flask import Blueprint, Flask, Response, jsonify

from dex_examples.workflow.engagement.engagement_input import EngagementInput
from dex_examples.workflow.money.transfer.transfer_request import TransferRequest
from dex_examples.workflow.subscription.customer import Customer
from dex_examples.workflow.subscription.subscription import Subscription
from sync_examples.app import SyncExampleApp
from sync_examples.config import DEFAULT_TIMEOUT, start_options
from sync_examples.query import (
    new_flow_id,
    optional_query,
    required_int_query,
    required_query,
    started_flow,
)


def create_app(app_state: SyncExampleApp) -> Flask:
    app = Flask(__name__)
    app.register_blueprint(_basic(app_state))
    app.register_blueprint(_money_transfer(app_state))
    app.register_blueprint(_engagement(app_state))
    app.register_blueprint(_subscription(app_state))
    app.register_blueprint(_patterns(app_state))

    @app.get("/health")
    def health() -> Response:
        return jsonify({"ok": True})

    return app


def _basic(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint("basic", __name__, url_prefix="/basic")

    @blueprint.get("/start")
    def start() -> Response:
        flow_id = required_query("workflowId")
        run_id = app_state.client.start_flow(
            app_state.basic,
            flow_id,
            required_int_query("inputNum"),
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/appendString")
    def append_string() -> str:
        return app_state.client.invoke_rpc(
            app_state.basic.append_string,
            required_query("workflowId"),
            required_query("str"),
        )

    @blueprint.get("/approve")
    def approve() -> str:
        app_state.client.invoke_rpc(
            app_state.basic.approve,
            required_query("workflowId"),
        )
        return "done"

    return blueprint


def _money_transfer(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint("money_transfer", __name__, url_prefix="/moneytransfer")

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


def _engagement(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint("engagement", __name__, url_prefix="/engagement")

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
    blueprint = Blueprint("subscription", __name__, url_prefix="/subscription")

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
        app_state.client.publish(
            required_query("workflowId"),
            app_state.subscription.cancel_subscription,
            None,
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


def _patterns(app_state: SyncExampleApp) -> Blueprint:
    blueprint = Blueprint("patterns", __name__, url_prefix="/design-pattern")

    @blueprint.get("/parentchild/start")
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

    @blueprint.get("/interruptible/start")
    def interruptible_start() -> Response:
        flow_id = optional_query("workflowId", new_flow_id("interruptible"))
        run_id = app_state.client.start_flow(
            app_state.interruptible,
            flow_id,
            None,
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/interruptible/cancel")
    def interruptible_cancel() -> str:
        app_state.client.invoke_rpc(
            app_state.interruptible.interrupt,
            required_query("workflowId"),
        )
        return "done"

    return blueprint
