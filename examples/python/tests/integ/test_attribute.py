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
from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.http_app import create_app
from tests.integ.conftest import WAIT_TIMEOUT

from dex import AsyncClient, FlowStatus

pytestmark = pytest.mark.integ


async def test_attribute_read_returns_committed_values_of_closed_run(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("primitive-attribute-read")
    await client.start_flow(app.attribute, flow_id, "read", start_options())
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.COMPLETED

    response = await create_app(app).test_client().get(
        "/primitives/attribute/read", query_string={"workflowId": flow_id}
    )

    assert response.status_code == 200, await response.get_data(as_text=True)
    assert await response.get_json() == {
        "status": "completed",
        "paymentProgress": "authorized",
        "email": None,
    }


async def test_attribute_read_returns_not_found_for_missing_flow(
    app: ExampleApp,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("primitive-attribute-read-missing")
    response = await create_app(app).test_client().get(
        "/primitives/attribute/read", query_string={"workflowId": flow_id}
    )

    assert response.status_code == 404, await response.get_data(as_text=True)
    body = await response.get_json()
    assert list(body) == ["error"]
    assert flow_id in body["error"]


async def test_attribute_read_requires_workflow_id(app: ExampleApp) -> None:
    response = await create_app(app).test_client().get("/primitives/attribute/read")

    assert response.status_code == 400, await response.get_data(as_text=True)
