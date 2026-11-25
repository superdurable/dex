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
"""Control Long-Running Work design pattern."""

from __future__ import annotations

from typing import Callable

import pytest
from dex import (
    AsyncClient,
    FlowStatus,
    StepExecutionId,
    WaitForStepCompletionOptions,
    WorkerInvocationError,
)

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from tests.integ.conftest import WAIT_TIMEOUT

pytestmark = pytest.mark.integ


async def wait_for_first_record_batch(client: AsyncClient, flow_id: str) -> None:
    await client.wait_for_step_completion(
        flow_id,
        StepExecutionId("ProcessRecordBatch"),
        WaitForStepCompletionOptions(request_timeout=WAIT_TIMEOUT),
    )


async def completed_output(client: AsyncClient, flow_id: str) -> str:
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.COMPLETED, result.error_message
    outputs = [completion.decode(str) for completion in result.completions]
    assert len(outputs) == 1, outputs
    return outputs[0]


async def test_control_long_running_work_reads_the_batch_size_each_batch(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    # At the default batch size the backfill needs 100 seconds, longer than the wait timeout.
    flow = app.record_backfill
    flow_id = new_flow_id("control-long-running-work-batch")
    await client.start_flow(flow, flow_id, None, start_options())

    for invalid_size in (0, 1001):
        with pytest.raises(WorkerInvocationError, match="batch size must be between 1 and 1000"):
            await client.invoke_rpc(flow.set_batch_size, flow_id, invalid_size)
    # The first batch has used the default size, so a later batch must read the new one.
    await wait_for_first_record_batch(client, flow_id)
    await client.invoke_rpc(flow.set_batch_size, flow_id, 1000)

    assert await completed_output(client, flow_id) == "processed 1000 records"


async def test_control_long_running_work_stops_before_the_next_batch(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = app.record_backfill
    flow_id = new_flow_id("control-long-running-work-stop")
    await client.start_flow(flow, flow_id, None, start_options())

    await wait_for_first_record_batch(client, flow_id)
    await client.invoke_rpc(flow.request_stop, flow_id)

    output = await completed_output(client, flow_id)
    prefix = "stopped before record "
    assert output.startswith(prefix), output
    assert int(output.removeprefix(prefix)) >= 11, output
