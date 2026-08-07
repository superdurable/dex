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

import random

from dex import DexException, ErrorSubStatus
from flask import Blueprint

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.controller.query import optional_query, required_query
from dex_examples.resourcecontrol.controller_flow import (
    SPOT_INSTANCE_IDS,
    ControllerFlow,
)
from dex_examples.resourcecontrol.request import Request


def create_resource_control_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("resource_control", __name__, url_prefix="/controller")

    @blueprint.get("/request")
    def enqueue_request() -> str:
        request = Request(required_query("id"), optional_query("data", "abcd"))
        # A real deployment would rank instances by usage instead of picking one
        # at random, then check availability before enqueueing.
        instance_id = random.choice(SPOT_INSTANCE_IDS)
        flow_id = f"controller_flow_{instance_id}"
        try:
            accepted = app_state.client.invoke_rpc(
                app_state.controller.enqueue,
                flow_id,
                request,
            )
        except DexException as error:
            if error.sub_status is not ErrorSubStatus.FLOW_NOT_EXISTS:
                raise
            app_state.client.start_flow(
                app_state.controller,
                flow_id,
                request,
                start_options().with_attribute(
                    ControllerFlow.instance_id,
                    instance_id,
                ),
            )
            accepted = True
        if accepted:
            return "request is accepted"
        return "request is denied because instance is busy. Please retry later"

    @blueprint.get("/shutdown")
    def shutdown() -> str:
        instance_id = required_query("instance_id")
        app_state.client.invoke_rpc(
            app_state.controller.shutdown,
            f"controller_flow_{instance_id}",
        )
        return "done"

    @blueprint.get("/processing/describe")
    def describe_processing() -> str:
        return app_state.client.invoke_rpc(
            app_state.processing.describe,
            f"processing-{required_query('id')}",
        )

    return blueprint
