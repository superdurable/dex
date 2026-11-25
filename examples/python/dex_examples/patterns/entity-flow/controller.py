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

from dex import StartFlowOptions
from quart import Blueprint, Response, abort, jsonify

from dex_examples.app import ExampleApp
from dex_examples.patterns.entity_flow.customer_account_flow import (
    ContactDetails,
    CreditAdjustment,
    customer_account_flow_id,
)
from dex_examples.shared.query import (
    required_body_field,
    required_int_body_field,
    required_query,
    started_flow,
)


def create_entity_flow_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_entity_flow",
        __name__,
        url_prefix="/patterns/entity-flow",
    )

    @blueprint.post("/account")
    async def open_account() -> Response:
        flow = app_state.customer_account
        account_id = await required_body_field("accountId")
        contact = await required_contact_details()
        flow_id = customer_account_flow_id(account_id)
        options = (
            StartFlowOptions()
            .with_attribute(flow.display_name, contact.display_name)
            .with_attribute(flow.email, contact.email)
            .with_attribute(flow.credit_balance, 0)
        )
        run_id = await app_state.client.start_flow(flow, flow_id, None, options)
        return started_flow(flow_id, run_id)

    @blueprint.post("/account/contact")
    async def update_contact_details() -> str:
        account_id = await required_body_field("accountId")
        contact = await required_contact_details()
        await app_state.client.invoke_rpc(
            app_state.customer_account.update_contact_details,
            customer_account_flow_id(account_id),
            contact,
        )
        return "Updated contact details"

    @blueprint.post("/account/credits")
    async def adjust_credit_balance() -> Response:
        account_id = await required_body_field("accountId")
        amount = await required_int_body_field("amount")
        balance = await app_state.client.invoke_rpc(
            app_state.customer_account.adjust_credit_balance,
            customer_account_flow_id(account_id),
            CreditAdjustment(amount),
        )
        return jsonify({"creditBalance": balance})

    @blueprint.get("/account")
    async def get_account() -> Response:
        flow = app_state.customer_account
        account_id = required_query("accountId")
        values = await app_state.client.get_attributes(
            customer_account_flow_id(account_id),
            flow.display_name,
            flow.email,
            flow.credit_balance,
        )
        return jsonify(
            {
                "accountId": account_id,
                "displayName": values.get(flow.display_name, ""),
                "email": values.get(flow.email, ""),
                "creditBalance": values.get(flow.credit_balance, 0),
            }
        )

    @blueprint.post("/account/retire")
    async def retire_account() -> str:
        await app_state.client.invoke_rpc(
            app_state.customer_account.retire_account,
            customer_account_flow_id(required_query("accountId")),
        )
        return "Retired account"

    return blueprint


async def required_contact_details() -> ContactDetails:
    contact = ContactDetails(
        await required_body_field("displayName"),
        await required_body_field("email"),
    )
    try:
        contact.validate()
    except ValueError as error:
        abort(400, description=str(error))
    return contact
