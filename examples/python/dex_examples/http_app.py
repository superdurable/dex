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

from dex import (
    DexServiceError,
    FlowAlreadyStartedError,
    FlowNotActiveError,
    FlowNotFoundError,
    LongPollTimeoutError,
)
from quart import Quart
from werkzeug.exceptions import HTTPException

from dex_examples.app import ExampleApp
from dex_examples.http_cors import install_quart_cors
from dex_examples.patterns.drain_channels.internal.controller import (
    create_drain_internal_blueprint,
)
from dex_examples.patterns.drain_channels.signal.controller import (
    create_drain_signal_blueprint,
)
from dex_examples.patterns.entity_store.controller import create_entity_store_blueprint
from dex_examples.patterns.interruptible.controller import create_interruptible_blueprint
from dex_examples.patterns.intervention.controller import create_intervention_blueprint
from dex_examples.patterns.parallel.controller import create_parallel_blueprint
from dex_examples.patterns.parent_child.controller import create_parent_child_blueprint
from dex_examples.patterns.polling.controller import create_polling_pattern_blueprint
from dex_examples.patterns.recovery.controller import create_recovery_blueprint
from dex_examples.patterns.reminders.controller import create_reminders_blueprint
from dex_examples.patterns.resettable_timer.controller import (
    create_resettable_timer_blueprint,
)
from dex_examples.patterns.resource_control.controller import (
    create_resource_control_blueprint,
)
from dex_examples.patterns.scalable_parallel.controller import (
    create_scalable_parallel_blueprint,
)
from dex_examples.patterns.timeout.controller import create_timeout_blueprint
from dex_examples.patterns.wait_for_state_completion.controller import (
    create_wait_for_state_completion_blueprint,
)
from dex_examples.primitives.attribute.controller import create_attribute_blueprint
from dex_examples.primitives.channel.controller import create_channel_blueprint
from dex_examples.primitives.client_apis.controller import create_client_apis_blueprint
from dex_examples.primitives.custom_retry.controller import create_custom_retry_blueprint
from dex_examples.primitives.durability.controller import create_durability_blueprint
from dex_examples.primitives.heartbeat.controller import create_heartbeat_blueprint
from dex_examples.primitives.options_override.controller import (
    create_options_override_blueprint,
)
from dex_examples.primitives.rpc.controller import create_rpc_blueprint
from dex_examples.primitives.step.controller import create_step_blueprint
from dex_examples.primitives.step_decision.controller import create_step_decision_blueprint
from dex_examples.primitives.subflow.controller import create_subflow_blueprint
from dex_examples.primitives.timer.controller import create_timer_blueprint
from dex_examples.primitives.wait_types.controller import create_wait_types_blueprint
from dex_examples.products.ai_agent_email.http_routes import (
    STATIC_DIR,
    TEMPLATE_DIR,
    create_ai_agent_blueprint,
)
from dex_examples.products.engagement.controller import create_engagement_blueprint
from dex_examples.products.job_post.controller import create_job_post_blueprint
from dex_examples.products.microservices.controller import create_microservice_blueprint
from dex_examples.products.money_transfer.controller import create_money_transfer_blueprint
from dex_examples.products.order_processing.controller import (
    create_order_processing_blueprint,
)
from dex_examples.products.polling.controller import create_polling_blueprint
from dex_examples.products.shortlist_candidates.controller import create_shortlist_blueprint
from dex_examples.products.signup.controller import create_signup_blueprint
from dex_examples.products.subscription.controller import create_subscription_blueprint

ERROR_HTTP_CODES = {
    FlowAlreadyStartedError: 409,
    FlowNotFoundError: 404,
    FlowNotActiveError: 409,
    LongPollTimeoutError: 504,
}


def create_app(app_state: ExampleApp) -> Quart:
    quart_app = Quart(
        __name__,
        template_folder=str(TEMPLATE_DIR),
        static_folder=str(STATIC_DIR),
        static_url_path="/static",
    )

    quart_app.register_blueprint(create_money_transfer_blueprint(app_state))
    quart_app.register_blueprint(create_order_processing_blueprint(app_state))
    quart_app.register_blueprint(create_microservice_blueprint(app_state))
    quart_app.register_blueprint(create_engagement_blueprint(app_state))
    quart_app.register_blueprint(create_subscription_blueprint(app_state))
    quart_app.register_blueprint(create_polling_blueprint(app_state))
    quart_app.register_blueprint(create_signup_blueprint(app_state))
    quart_app.register_blueprint(create_job_post_blueprint(app_state))
    quart_app.register_blueprint(create_shortlist_blueprint(app_state))
    quart_app.register_blueprint(create_ai_agent_blueprint(app_state))

    quart_app.register_blueprint(create_polling_pattern_blueprint(app_state))
    quart_app.register_blueprint(create_interruptible_blueprint(app_state))
    quart_app.register_blueprint(create_reminders_blueprint(app_state))
    quart_app.register_blueprint(create_entity_store_blueprint(app_state))
    quart_app.register_blueprint(create_intervention_blueprint(app_state))
    quart_app.register_blueprint(create_resettable_timer_blueprint(app_state))
    quart_app.register_blueprint(create_parallel_blueprint(app_state))
    quart_app.register_blueprint(create_recovery_blueprint(app_state))
    quart_app.register_blueprint(create_scalable_parallel_blueprint(app_state))
    quart_app.register_blueprint(create_parent_child_blueprint(app_state))
    quart_app.register_blueprint(create_drain_internal_blueprint(app_state))
    quart_app.register_blueprint(create_drain_signal_blueprint(app_state))
    quart_app.register_blueprint(create_wait_for_state_completion_blueprint(app_state))
    quart_app.register_blueprint(create_timeout_blueprint(app_state))
    quart_app.register_blueprint(create_resource_control_blueprint(app_state))

    quart_app.register_blueprint(create_step_blueprint(app_state))
    quart_app.register_blueprint(create_custom_retry_blueprint(app_state))
    quart_app.register_blueprint(create_durability_blueprint(app_state))
    quart_app.register_blueprint(create_heartbeat_blueprint(app_state))
    quart_app.register_blueprint(create_options_override_blueprint(app_state))
    quart_app.register_blueprint(create_step_decision_blueprint(app_state))
    quart_app.register_blueprint(create_wait_types_blueprint(app_state))
    quart_app.register_blueprint(create_attribute_blueprint(app_state))
    quart_app.register_blueprint(create_channel_blueprint(app_state))
    quart_app.register_blueprint(create_timer_blueprint(app_state))
    quart_app.register_blueprint(create_rpc_blueprint(app_state))
    quart_app.register_blueprint(create_subflow_blueprint(app_state))
    quart_app.register_blueprint(create_client_apis_blueprint(app_state))

    install_quart_cors(quart_app)
    quart_app.register_error_handler(HTTPException, handle_http_exception)
    quart_app.register_error_handler(DexServiceError, handle_dex_exception)
    quart_app.register_error_handler(Exception, handle_unexpected_exception)

    @quart_app.get("/")
    def index() -> str:
        return "dex examples home"

    return quart_app


def handle_http_exception(error: HTTPException) -> tuple[str, int]:
    return error.description or error.name, error.code or 500


def handle_dex_exception(error: DexServiceError) -> tuple[str, int]:
    status = next(
        (
            status
            for error_type, status in ERROR_HTTP_CODES.items()
            if isinstance(error, error_type)
        ),
        500,
    )
    return error.detail, status


def handle_unexpected_exception(error: Exception) -> tuple[str, int]:
    del error
    return traceback.format_exc(), 500
