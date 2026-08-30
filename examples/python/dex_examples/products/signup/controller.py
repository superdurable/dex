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

from dex import FlowAlreadyStartedError
from quart import Blueprint

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.shared.query import required_query
from dex_examples.products.signup.signup_form import SignupForm


def create_signup_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("signup", __name__, url_prefix="/products/signup")

    @blueprint.get("/submit")
    async def submit() -> str:
        username = required_query("username")
        form = SignupForm(username, required_query("email"), "Test", "Test")
        try:
            await app_state.client.start_flow(
                app_state.user_onboarding,
                username,
                form,
                start_options(),
            )
        except FlowAlreadyStartedError:
            return "username already started registry"
        return "success"

    @blueprint.get("/verify")
    async def verify() -> str:
        return await app_state.client.invoke_rpc(
            app_state.user_onboarding.verify,
            required_query("username"),
        )

    @blueprint.get("/accomplish-task-1")
    async def accomplish_task_1() -> str:
        return await app_state.client.invoke_rpc(
            app_state.user_onboarding.accomplish_task_1,
            required_query("username"),
        )

    @blueprint.get("/accomplish-task-2")
    async def accomplish_task_2() -> str:
        return await app_state.client.invoke_rpc(
            app_state.user_onboarding.accomplish_task_2,
            required_query("username"),
        )

    return blueprint
