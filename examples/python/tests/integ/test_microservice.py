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

from typing import Callable

import pytest
from dex import Client

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.workflow.microservices.orchestration_flow import OrchestrationFlow
from tests.integ.conftest import WAIT_TIMEOUT, attribute_or_none, wait_until

pytestmark = pytest.mark.integ

INITIAL_DATA = "test initial data"


def test_orchestration_completes_when_ready_is_published(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("microservice")
    client.start_flow(app.orchestration, flow_id, INITIAL_DATA, start_options())

    wait_until(
        "CallAPI1 to publish the shared data attribute",
        lambda: attribute_or_none(client, flow_id, OrchestrationFlow.data)
        == INITIAL_DATA,
    )
    client.publish(flow_id, app.orchestration.ready, None)

    assert client.wait_for_flow(flow_id, str, WAIT_TIMEOUT) == INITIAL_DATA


def test_orchestration_swap_replaces_the_data_before_completion(
    app: ExampleApp,
    client: Client,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("microservice")
    client.start_flow(app.orchestration, flow_id, INITIAL_DATA, start_options())

    wait_until(
        "CallAPI1 to publish the shared data attribute",
        lambda: attribute_or_none(client, flow_id, OrchestrationFlow.data)
        == INITIAL_DATA,
    )
    assert client.invoke_rpc(app.orchestration.swap, flow_id, "swapped data") == (
        INITIAL_DATA
    )

    client.publish(flow_id, app.orchestration.ready, None)
    assert client.wait_for_flow(flow_id, str, WAIT_TIMEOUT) == "swapped data"
