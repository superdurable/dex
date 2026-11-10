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

"""The Quart app that serves every agent's session routes."""

from __future__ import annotations

from dex import DexServiceError
from quart import Quart
from werkzeug.exceptions import HTTPException

from agent_harness_examples.app import AgentHarnessExamplesApp
from agent_harness_examples.http_routes import create_agent_session_blueprint


def create_app(app_state: AgentHarnessExamplesApp) -> Quart:
    quart_app = Quart(__name__)
    for example in app_state.examples:
        quart_app.register_blueprint(create_agent_session_blueprint(app_state, example))
    quart_app.register_error_handler(HTTPException, _handle_http_exception)
    quart_app.register_error_handler(DexServiceError, _handle_dex_exception)
    return quart_app


def _handle_http_exception(error: HTTPException) -> tuple[str, int]:
    return error.description or error.name, error.code or 500


def _handle_dex_exception(error: DexServiceError) -> tuple[str, int]:
    return error.detail, 500
