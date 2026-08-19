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
from datetime import datetime, timedelta

from dex import FlowConfig, StartFlowOptions
from quart import Blueprint, Response, abort, jsonify

from dex_examples.app import ExampleApp
from dex_examples.patterns.entity_store.user_profile import (
    UserProfileMetadata,
    UserProfileRequest,
)
from dex_examples.patterns.entity_store.user_profile_flow import STORE_NAME
from dex_examples.shared.query import (
    required_body_field,
    required_bool_body_field,
    required_float_body_field,
    required_int_body_field,
    required_object_body_field,
    required_query,
)


def create_entity_store_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("pattern_entity_store", __name__, url_prefix="/patterns/entity-store")

    @blueprint.post("/profile")
    async def create_user_profile() -> str:
        profile_request = UserProfileRequest(
            await required_body_field("userId"),
            await required_body_field("displayName"),
            await required_body_field("email"),
            await required_bool_body_field("marketingOptIn"),
            await required_int_body_field("credits"),
            await required_float_body_field("weight"),
            datetime.fromisoformat(await required_body_field("lastLoggedInTime")),
            await required_user_profile_metadata(),
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
            .with_attribute(app_state.user_profile.credits, profile.credits)
            .with_attribute(app_state.user_profile.weight, profile.weight)
            .with_attribute(
                app_state.user_profile.last_logged_in_time,
                profile.last_logged_in_time,
            )
            .with_attribute(app_state.user_profile.metadata, profile.metadata)
        )
        return await app_state.client.start_flow(
            app_state.user_profile,
            profile_request.user_id,
            None,
            options,
        )

    @blueprint.post("/profile/update")
    async def update_user_profile() -> str:
        profile_request = UserProfileRequest(
            await required_body_field("userId"),
            await required_body_field("displayName"),
            await required_body_field("email"),
            await required_bool_body_field("marketingOptIn"),
            await required_int_body_field("credits"),
            await required_float_body_field("weight"),
            datetime.fromisoformat(await required_body_field("lastLoggedInTime")),
            await required_user_profile_metadata(),
        )
        await app_state.client.invoke_rpc(
            app_state.user_profile.update_profile,
            profile_request.user_id,
            profile_request.profile(),
        )
        return "Updated user profile"

    @blueprint.get("/profile")
    async def get_user_profile() -> Response:
        profile = await app_state.client.invoke_rpc(
            app_state.user_profile.get_profile,
            required_query("userId"),
        )
        return jsonify(asdict(profile))

    @blueprint.post("/profile/clear")
    async def clear_user_profile() -> str:
        await app_state.client.invoke_rpc(
            app_state.user_profile.clear_profile,
            required_query("userId"),
        )
        return "Cleared user profile"

    return blueprint


async def required_user_profile_metadata() -> UserProfileMetadata:
    value = await required_object_body_field("metadata")
    source = value.get("source")
    tags = value.get("tags")
    if not isinstance(source, str) or not source:
        abort(400, description="metadata.source must be a non-empty string")
    if not isinstance(tags, list) or not all(isinstance(tag, str) for tag in tags):
        abort(400, description="metadata.tags must be an array of strings")
    return UserProfileMetadata(source, tags)
