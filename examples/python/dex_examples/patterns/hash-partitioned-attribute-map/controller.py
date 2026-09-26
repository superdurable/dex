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

from dex import RPCInvokeOptions, StartFlowOptions
from quart import Blueprint, Response, abort, jsonify, request

from dex_examples.app import ExampleApp
from dex_examples.patterns.hash_partitioned_attribute_map.customer_directory_flow import (
    FLOW_ID,
    CustomerProfile,
    email_partition,
)


def create_hash_partitioned_attribute_map_blueprint(
    app_state: ExampleApp,
) -> Blueprint:
    blueprint = Blueprint(
        "pattern_hash_partitioned_attribute_map",
        __name__,
        url_prefix="/patterns/hash-partitioned-attribute-map",
    )

    @blueprint.post("/start")
    async def start() -> Response:
        run_id = await app_state.client.start_flow(
            app_state.customer_directory,
            FLOW_ID,
            None,
            StartFlowOptions(ignore_already_started=True),
        )
        return jsonify({"flowID": FLOW_ID, "runID": run_id})

    @blueprint.put("/customer-profile")
    async def upsert_customer_profile() -> Response:
        body = await request.get_json(silent=True)
        if not isinstance(body, dict):
            abort(400, description="JSON request body is required")
        profile = CustomerProfile(
            _required_string(body, "emailAddress"),
            _required_string(body, "fullName"),
            _required_string(body, "companyName"),
            _required_string(body, "customerTier"),
        )
        try:
            _, partition_name, _ = email_partition(profile.email_address)
        except ValueError as error:
            abort(400, description=str(error))
        flow = app_state.customer_directory
        saved_profile = await app_state.client.invoke_rpc(
            flow.upsert_customer_profile,
            FLOW_ID,
            profile,
            options=RPCInvokeOptions(
                lock_attribute_map_instances=(
                    flow.customer_profiles_by_email_partition.lock(partition_name),
                ),
                load_attribute_map_instances=(
                    flow.customer_profiles_by_email_partition.load(partition_name),
                ),
            ),
        )
        return jsonify(_profile_json(saved_profile))

    @blueprint.get("/customer-profile")
    async def get_customer_profile() -> Response:
        email_address = request.args.get("emailAddress", "")
        try:
            _, partition_name, _ = email_partition(email_address)
        except ValueError as error:
            abort(400, description=str(error))
        flow = app_state.customer_directory
        profile = await app_state.client.invoke_rpc(
            flow.get_customer_profile_by_email,
            FLOW_ID,
            email_address,
            options=RPCInvokeOptions(
                load_attribute_map_instances=(
                    flow.customer_profiles_by_email_partition.load(partition_name),
                )
            ),
        )
        return jsonify(_profile_json(profile))

    return blueprint


def _required_string(body: dict[str, object], name: str) -> str:
    value = body.get(name)
    if not isinstance(value, str) or not value:
        abort(400, description=f"{name} is required")
    return value


def _profile_json(profile: CustomerProfile) -> dict[str, str]:
    return {
        "emailAddress": profile.email_address,
        "fullName": profile.full_name,
        "companyName": profile.company_name,
        "customerTier": profile.customer_tier,
    }
