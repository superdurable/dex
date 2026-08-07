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

from flask import Blueprint, Response

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.controller.query import (
    new_flow_id,
    optional_query,
    required_int_query,
    required_query,
    started_flow,
)
from dex_examples.workflow.money.transfer.transfer_request import TransferRequest


def create_money_transfer_blueprint(app_state: ExampleApp) -> Blueprint:
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
