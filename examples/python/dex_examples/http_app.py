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

import traceback

from dex import DexException, ErrorSubStatus
from quart import Quart
from werkzeug.exceptions import HTTPException

from dex_examples.ai_agent_email.http_routes import (
    STATIC_DIR,
    TEMPLATE_DIR,
    create_ai_agent_blueprint,
)
from dex_examples.app import ExampleApp
from dex_examples.controller.basic_controller import create_basic_blueprint
from dex_examples.controller.engagement_controller import create_engagement_blueprint
from dex_examples.controller.job_post_controller import create_job_post_blueprint
from dex_examples.controller.microservice_controller import create_microservice_blueprint
from dex_examples.controller.money_transfer_controller import (
    create_money_transfer_blueprint,
)
from dex_examples.controller.polling_controller import create_polling_blueprint
from dex_examples.controller.resourcecontrol_controller import (
    create_resource_control_blueprint,
)
from dex_examples.controller.shortlist_controller import create_shortlist_blueprint
from dex_examples.controller.signup_controller import create_signup_blueprint
from dex_examples.controller.subscription_controller import create_subscription_blueprint
from dex_examples.patterns.controller.design_pattern_controller import (
    create_design_pattern_blueprint,
)

SUB_STATUS_HTTP_CODES = {
    ErrorSubStatus.FLOW_ALREADY_STARTED: 409,
    ErrorSubStatus.FLOW_NOT_EXISTS: 404,
    ErrorSubStatus.LONG_POLL_TIMEOUT: 504,
}


def create_app(app_state: ExampleApp) -> Quart:
    quart_app = Quart(
        __name__,
        template_folder=str(TEMPLATE_DIR),
        static_folder=str(STATIC_DIR),
        static_url_path="/static",
    )

    quart_app.register_blueprint(create_money_transfer_blueprint(app_state))
    quart_app.register_blueprint(create_microservice_blueprint(app_state))
    quart_app.register_blueprint(create_engagement_blueprint(app_state))
    quart_app.register_blueprint(create_subscription_blueprint(app_state))
    quart_app.register_blueprint(create_polling_blueprint(app_state))
    quart_app.register_blueprint(create_signup_blueprint(app_state))
    quart_app.register_blueprint(create_job_post_blueprint(app_state))
    quart_app.register_blueprint(create_shortlist_blueprint(app_state))
    quart_app.register_blueprint(create_basic_blueprint(app_state))
    quart_app.register_blueprint(create_resource_control_blueprint(app_state))
    quart_app.register_blueprint(create_design_pattern_blueprint(app_state))
    quart_app.register_blueprint(create_ai_agent_blueprint(app_state))

    quart_app.register_error_handler(HTTPException, handle_http_exception)
    quart_app.register_error_handler(DexException, handle_dex_exception)
    quart_app.register_error_handler(Exception, handle_unexpected_exception)

    @quart_app.get("/")
    def index() -> str:
        return "dex examples home"

    return quart_app


def handle_http_exception(error: HTTPException) -> tuple[str, int]:
    return error.description or error.name, error.code or 500


def handle_dex_exception(error: DexException) -> tuple[str, int]:
    status = 500
    if error.sub_status is not None:
        status = SUB_STATUS_HTTP_CODES.get(error.sub_status, 500)
    return error.detail or str(error), status


def handle_unexpected_exception(error: Exception) -> tuple[str, int]:
    # Returning the traceback lets the Dex Web UI show the failure in full.
    del error
    return traceback.format_exc(), 500
