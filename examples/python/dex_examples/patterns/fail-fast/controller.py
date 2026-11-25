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

from quart import Blueprint, Response, abort, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.patterns.fail_fast.expense_reimbursement_flow import PAYOUT_OUTCOME
from dex_examples.patterns.fail_fast.payment_provider import ExpenseClaim
from dex_examples.shared.query import required_int_query, required_query


def create_fail_fast_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_fail_fast", __name__, url_prefix="/patterns/fail-fast"
    )

    @blueprint.get("/start")
    async def start_expense_reimbursement() -> str:
        return await app_state.client.start_flow(
            app_state.expense_reimbursement,
            required_query("workflowId"),
            ExpenseClaim(
                required_int_query("amountCents"), required_query("bankAccount")
            ),
            start_options(),
        )

    @blueprint.get("/get-outcome")
    async def get_payout_outcome() -> Response:
        values = await app_state.client.get_attributes(
            required_query("workflowId"), PAYOUT_OUTCOME
        )
        outcome = values.get(PAYOUT_OUTCOME)
        if outcome is None:
            abort(404, description="the claim has no outcome yet")
        return jsonify(asdict(outcome))

    return blueprint
