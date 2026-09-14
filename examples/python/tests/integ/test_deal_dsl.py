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

from typing import Callable

import pytest
from dex import AsyncClient, AttributeMatch, WaitForAttributeOptions
from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.products.deal_dsl.deal_dsl_flow import (
    ConditionMessage,
    example_deal_start,
)
from tests.integ.conftest import WAIT_TIMEOUT

pytestmark = pytest.mark.integ


async def test_deal_dsl_completes_an_item_purchase(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("deal-dsl")
    await client.start_flow(
        app.deal_dsl,
        flow_id,
        example_deal_start("buyer-1"),
        start_options(),
    )
    assert (
        await client.wait_for_attribute_match(
            flow_id,
            app.deal_dsl.current_state,
            AttributeMatch.equal_to("negotiating"),
            WaitForAttributeOptions(maximum_wait_time=WAIT_TIMEOUT),
        )
        == "negotiating"
    )
    await client.invoke_rpc(
        app.deal_dsl.send_condition_message,
        flow_id,
        ConditionMessage("buyer-decision", {"accepted": "true"}),
    )
    result = (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        dict[str, str]
    )
    assert result["lastAction"] == "deliverItemToBuyer"
    assert result["itemDeliveryStatus"] == "delivered"
