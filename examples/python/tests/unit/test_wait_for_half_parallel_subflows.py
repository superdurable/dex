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

from types import SimpleNamespace
from unittest.mock import Mock

import grpc
import pytest
from dex import (
    AsyncClient,
    AsyncContext,
    ErrorSubStatus,
    FlowClosingError,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    FlowResult,
    FlowStatus,
    SubFlow,
)

from dex_examples.patterns.parallel_subflows.example_subflow import ExampleSubFlow
from dex_examples.patterns.parallel_subflows.wait_for_half_parent_flow import (
    CHILD_STOP_OUTCOMES,
    ChildStopOutcome,
    WaitForHalfParentFlow,
)


CHILD_FLOW_ID = "child-flow"


def flow_error(error_type, operation: str):
    return error_type(
        grpc.StatusCode.FAILED_PRECONDITION,
        ErrorSubStatus.UNCATEGORIZED,
        "child stop race",
        operation,
        CHILD_FLOW_ID,
    )


@pytest.fixture
def child_step(monkeypatch):
    client = Mock(spec=AsyncClient)
    flow = WaitForHalfParentFlow(lambda: client, ExampleSubFlow())
    context = Mock(spec=AsyncContext)
    record = Mock()
    monkeypatch.setattr(SubFlow, "get_flow_id", lambda _: CHILD_FLOW_ID)
    monkeypatch.setattr(SubFlow, "get_condition_results", lambda _: FlowResult(FlowStatus.RUNNING, []))
    monkeypatch.setattr(type(CHILD_STOP_OUTCOMES), "set", record)
    return flow.subflow, context, client, record


async def test_successful_stop_records_stopped(child_step):
    step, context, client, record = child_step
    await step.execute(context, "request")
    client.stop_flow.assert_awaited_once_with(CHILD_FLOW_ID)
    client.describe_flow.assert_not_awaited()
    record.assert_called_once_with(context, CHILD_FLOW_ID, ChildStopOutcome("stopped"))


@pytest.mark.parametrize("error_type", [FlowNotFoundError, FlowNotActiveOrNotFoundError, FlowClosingError])
@pytest.mark.parametrize("status,output", [(FlowStatus.COMPLETED, ""), (FlowStatus.CANCELED, None)])
async def test_refused_stop_records_closed_child(child_step, error_type, status, output):
    step, context, client, record = child_step
    client.stop_flow.side_effect = flow_error(error_type, "stop_flow")
    client.describe_flow.return_value = SimpleNamespace(status=status)
    result = Mock(spec=FlowResult, status=status, completions=[] if output is None else [Mock()])
    result.single_output.return_value = output
    client.wait_for_flow.return_value = result

    await step.execute(context, "request")

    client.describe_flow.assert_awaited_once_with(CHILD_FLOW_ID)
    client.wait_for_flow.assert_awaited_once_with(CHILD_FLOW_ID)
    record.assert_called_once_with(context, CHILD_FLOW_ID, ChildStopOutcome(status.value, output))


@pytest.mark.parametrize("missing", [False, True])
async def test_refused_stop_leaves_unconfirmed_child_unknown(child_step, missing):
    step, context, client, record = child_step
    client.stop_flow.side_effect = flow_error(FlowClosingError, "stop_flow")
    if missing:
        client.describe_flow.side_effect = flow_error(FlowNotFoundError, "describe_flow")
    else:
        client.describe_flow.return_value = SimpleNamespace(status=FlowStatus.RUNNING)

    await step.execute(context, "request")

    client.describe_flow.assert_awaited_once_with(CHILD_FLOW_ID)
    client.wait_for_flow.assert_not_awaited()
    record.assert_called_once_with(context, CHILD_FLOW_ID, ChildStopOutcome("unknown"))


@pytest.mark.parametrize("operation", ["stop_flow", "describe_flow", "wait_for_flow"])
async def test_unexpected_error_is_left_for_step_retry(child_step, operation):
    step, context, client, record = child_step
    failure = RuntimeError("service unavailable")
    client.stop_flow.side_effect = flow_error(FlowClosingError, "stop_flow")
    client.describe_flow.return_value = SimpleNamespace(status=FlowStatus.COMPLETED)
    getattr(client, operation).side_effect = failure

    with pytest.raises(RuntimeError) as raised:
        await step.execute(context, "request")

    assert raised.value is failure
    record.assert_not_called()
