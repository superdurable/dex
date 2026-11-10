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

"""Deferred choice, critical section, and cancel case design patterns."""

from __future__ import annotations

from typing import Any, Awaitable, Callable

import pytest
from dex import (
    AsyncClient,
    DexServiceError,
    Flow,
    FlowResult,
    FlowStatus,
    StartFlowOptions,
    StepExecutionId,
    TimerId,
    WaitForStepCompletionOptions,
)

from dex_examples.app import ExampleApp
from dex_examples.patterns.cancel_case.loan_application_flow import (
    STOPPED_CREDIT_CHECK_FLOW_ID,
    LoanApplication,
)
from dex_examples.patterns.critical_section.contract_edit_flow import (
    ContractEdit,
    ContractEditRecord,
)
from tests.integ.conftest import WAIT_TIMEOUT, wait_for_flow_status, wait_until

pytestmark = pytest.mark.integ


async def start_pattern_flow(
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    flow: Flow[Any],
    prefix: str,
    input: object,
) -> str:
    flow_id = new_flow_id(prefix)
    await client.start_flow(flow, flow_id, input, StartFlowOptions())
    return flow_id


async def wait_for_completed_flow(client: AsyncClient, flow_id: str) -> FlowResult:
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.COMPLETED, result.error_message
    return result


async def wait_for_step(client: AsyncClient, flow_id: str, step_type: str) -> None:
    await client.wait_for_step_completion(
        flow_id,
        StepExecutionId(step_type),
        WaitForStepCompletionOptions(request_timeout=WAIT_TIMEOUT),
    )


def outputs_of(result: FlowResult, step_type: str, output_type: type[Any]) -> list[Any]:
    return [
        completion.decode(output_type)
        for completion in result.completions
        if completion.step_type == step_type
    ]


async def skip_offer_expiry(client: AsyncClient, flow_id: str) -> None:
    """Retries until the Timer of AwaitOfferResponse is registered."""
    last_errors: list[DexServiceError] = []

    async def is_timer_skipped() -> bool:
        try:
            await client.skip_timer(
                flow_id,
                StepExecutionId("AwaitOfferResponse"),
                TimerId(condition_index=0),
            )
        except DexServiceError as error:
            last_errors.append(error)
            return False
        return True

    try:
        await wait_until("the offer expiry Timer to be skipped", is_timer_skipped)
    except AssertionError as error:
        raise AssertionError(f"{error}: {last_errors[-1:]}") from error


@pytest.mark.parametrize(
    ("respond", "expected_step", "expected_output"),
    [
        ("accept", "FulfillOffer", "fulfilled offer-1"),
        ("reject", "CloseRejectedOffer", "rejected offer-1"),
        ("expire", "ExpireOffer", "expired offer-1"),
    ],
)
async def test_deferred_choice_routes_on_the_customer_response(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    respond: str,
    expected_step: str,
    expected_output: str,
) -> None:
    flow = app.offer_response
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "pattern-deferred-choice", "offer-1"
    )
    responses: dict[str, Callable[[], Awaitable[object]]] = {
        "accept": lambda: client.invoke_rpc(flow.accept_offer, flow_id, "looks good"),
        "reject": lambda: client.invoke_rpc(flow.reject_offer, flow_id, "too expensive"),
        "expire": lambda: skip_offer_expiry(client, flow_id),
    }
    await responses[respond]()
    result = await wait_for_completed_flow(client, flow_id)
    assert [completion.step_type for completion in result.completions] == [expected_step]
    assert outputs_of(result, expected_step, str) == [expected_output]


async def test_deferred_choice_consumes_only_the_winning_response(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = app.offer_response
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "pattern-deferred-choice-race", "offer-2"
    )
    await client.invoke_rpc(flow.accept_offer, flow_id, "yes")
    await client.invoke_rpc(flow.reject_offer, flow_id, "no")
    result = await wait_for_completed_flow(client, flow_id)
    assert len(result.completions) == 1
    assert result.completions[0].step_type in ("FulfillOffer", "CloseRejectedOffer")


async def test_critical_section_runs_one_edit_at_a_time(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client,
        new_flow_id,
        app.contract_edit,
        "pattern-critical-section",
        [ContractEdit("legal"), ContractEdit("finance"), ContractEdit("procurement")],
    )
    records = await contract_edit_records(client, flow_id)
    assert sorted(record.reviewer for record in records) == [
        "finance",
        "legal",
        "procurement",
    ]
    assert all(record.outcome == "saved" for record in records)
    assert_contract_edits_do_not_overlap(records)


async def test_critical_section_returns_the_token_when_an_edit_fails(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client,
        new_flow_id,
        app.contract_edit,
        "pattern-critical-section-failure",
        [ContractEdit("legal", should_fail_edit=True), ContractEdit("finance")],
    )
    records = await contract_edit_records(client, flow_id)
    outcomes = {record.reviewer: record.outcome for record in records}
    assert outcomes == {"legal": "failed", "finance": "saved"}
    assert_contract_edits_do_not_overlap(records)


async def test_cancel_case_stops_the_running_child_flow(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = app.loan_application
    flow_id = await start_pattern_flow(
        client,
        new_flow_id,
        flow,
        "pattern-cancel-case",
        LoanApplication(applicant_id="applicant-1", credit_report_delay_seconds=3600),
    )
    await client.invoke_rpc(flow.cancel_application, flow_id, "applicant withdrew")
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.FAILED, result.error_message
    assert "application canceled: applicant withdrew" in (result.error_message or "")
    values = await client.get_attributes(flow_id, STOPPED_CREDIT_CHECK_FLOW_ID)
    child_flow_id = values.get(STOPPED_CREDIT_CHECK_FLOW_ID)
    assert child_flow_id
    await wait_for_flow_status(client, child_flow_id, FlowStatus.CANCELED)


async def test_cancel_case_after_the_child_flow_finished(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = app.loan_application
    flow_id = await start_pattern_flow(
        client,
        new_flow_id,
        flow,
        "pattern-cancel-case-late",
        LoanApplication(applicant_id="applicant-2", credit_report_delay_seconds=1),
    )
    await wait_for_step(client, flow_id, "RunCreditCheck")
    await client.invoke_rpc(flow.cancel_application, flow_id, "applicant withdrew")
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.FAILED, result.error_message
    assert "application canceled: applicant withdrew" in (result.error_message or "")


async def test_cancel_case_completes_with_the_underwriter_decision(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = app.loan_application
    flow_id = await start_pattern_flow(
        client,
        new_flow_id,
        flow,
        "pattern-cancel-case-decided",
        LoanApplication(applicant_id="applicant-3", credit_report_delay_seconds=1),
    )
    await wait_for_step(client, flow_id, "RunCreditCheck")
    await client.invoke_rpc(flow.decide_application, flow_id, "approved")
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "AwaitUnderwriterDecision", str) == [
        "approved after credit report for applicant-3"
    ]


async def contract_edit_records(
    client: AsyncClient, flow_id: str
) -> list[ContractEditRecord]:
    result = await wait_for_completed_flow(client, flow_id)
    outputs = outputs_of(result, "FinishContractEdits", list[ContractEditRecord])
    assert len(outputs) == 1
    records: list[ContractEditRecord] = outputs[0]
    return records


def assert_contract_edits_do_not_overlap(records: list[ContractEditRecord]) -> None:
    for left_index, left in enumerate(records):
        for right in records[left_index + 1 :]:
            is_disjoint = (
                left.finished_at_millis <= right.started_at_millis
                or right.finished_at_millis <= left.started_at_millis
            )
            assert is_disjoint, f"{left} overlaps {right}"
