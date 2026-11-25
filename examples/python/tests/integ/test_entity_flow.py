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

import asyncio
from typing import Callable

import pytest
from dex import (
    AsyncClient,
    FlowAlreadyStartedError,
    FlowStatus,
    RpcLockConflictError,
    StartFlowOptions,
    WorkerInvocationError,
)

from dex_examples.app import ExampleApp
from dex_examples.patterns.entity_flow.customer_account_flow import (
    ContactDetails,
    CreditAdjustment,
    customer_account_flow_id,
)
from tests.integ.conftest import WAIT_TIMEOUT

pytestmark = pytest.mark.integ

CONCURRENT_DEPOSITS = 8


async def test_customer_account_entity_flow_lifecycle(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = app.customer_account
    flow_id = customer_account_flow_id(new_flow_id("entity-flow"))
    options = (
        StartFlowOptions()
        .with_attribute(flow.display_name, "Ada")
        .with_attribute(flow.email, "ada@example.com")
        .with_attribute(flow.credit_balance, 0)
    )
    await client.start_flow(flow, flow_id, None, options)
    with pytest.raises(FlowAlreadyStartedError):
        await client.start_flow(flow, flow_id, None, options)

    await client.invoke_rpc(
        flow.update_contact_details,
        flow_id,
        ContactDetails("Ada Lovelace", "ada@lovelace.example"),
    )
    with pytest.raises(WorkerInvocationError, match="email must contain @"):
        await client.invoke_rpc(
            flow.update_contact_details,
            flow_id,
            ContactDetails("Ada", "not-an-email"),
        )

    await asyncio.gather(
        *(_deposit_with_retry(app, client, flow_id, 5) for _ in range(CONCURRENT_DEPOSITS))
    )
    with pytest.raises(WorkerInvocationError, match="below zero"):
        await client.invoke_rpc(
            flow.adjust_credit_balance, flow_id, CreditAdjustment(-1000)
        )

    values = await client.get_attributes(
        flow_id, flow.display_name, flow.email, flow.credit_balance
    )
    assert values[flow.display_name] == "Ada Lovelace"
    assert values[flow.email] == "ada@lovelace.example"
    assert values[flow.credit_balance] == 5 * CONCURRENT_DEPOSITS

    with pytest.raises(WorkerInvocationError, match="before retiring"):
        await client.invoke_rpc(flow.retire_account, flow_id)
    assert (await client.describe_flow(flow_id)).status == FlowStatus.RUNNING

    await client.invoke_rpc(
        flow.adjust_credit_balance,
        flow_id,
        CreditAdjustment(-5 * CONCURRENT_DEPOSITS),
    )
    await client.invoke_rpc(flow.retire_account, flow_id)
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.COMPLETED


async def _deposit_with_retry(
    app: ExampleApp,
    client: AsyncClient,
    flow_id: str,
    amount: int,
) -> None:
    for attempt in range(50):
        try:
            await client.invoke_rpc(
                app.customer_account.adjust_credit_balance,
                flow_id,
                CreditAdjustment(amount),
            )
            return
        except RpcLockConflictError:
            await asyncio.sleep(0.05 * (attempt + 1))
    raise AssertionError("credit deposit kept conflicting on the balance lock")
