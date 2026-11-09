# Copyright (c) 2026 Super Durable, Inc.
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

"""Each test asserts the defining behavior of one Workflow Pattern (WCP)."""

from __future__ import annotations

import asyncio
from typing import Any, Awaitable, Callable, TypeVar

import pytest
from dex import (
    AsyncClient,
    Attribute,
    Flow,
    FlowResult,
    FlowStatus,
    RpcLockConflictError,
    StartFlowOptions,
    StepExecutionId,
    StopFlowOptions,
    StopType,
    WaitForStepCompletionOptions,
)

from dex_examples.app import ExampleApp
from dex_examples.patterns.workflow_patterns.and_join_and_threads import (
    InvoicePaymentMatch,
    InvoicePaymentMatching,
)
from dex_examples.patterns.workflow_patterns.cancellation import (
    FINISHED_TRANSLATION_COUNT,
)
from dex_examples.patterns.workflow_patterns.iteration import (
    ArticleSubmission,
    BatchProgress,
)
from dex_examples.patterns.workflow_patterns.multi_choice_and_merges import (
    NotificationChoice,
)
from dex_examples.patterns.workflow_patterns.multiple_instances import (
    BidJoin,
    PartialInspection,
)
from dex_examples.patterns.workflow_patterns.partial_joins import (
    BlockingJoinRound,
    ProcessedRound,
)
from dex_examples.patterns.workflow_patterns.state_based import (
    IS_CHANGE_WINDOW_OPEN,
    TaskInterval,
)
from dex_examples.patterns.workflow_patterns.triggers import IS_AWAITING_MOTION_EVENT
from dex_examples.patterns.workflow_patterns.workflow_pattern_flows import (
    WorkflowPatternFlows,
)
from tests.integ.conftest import WAIT_TIMEOUT, flow_status_or_none, wait_until

pytestmark = pytest.mark.integ

OutputT = TypeVar("OutputT")
LOCK_CONFLICT_RETRY_SECONDS = 0.1


@pytest.fixture()
def patterns(app: ExampleApp) -> WorkflowPatternFlows:
    return app.workflow_patterns


async def start_pattern_flow(
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    flow: Flow[Any],
    prefix: str,
    input: object,
) -> str:
    flow_id = new_flow_id(f"wcp-{prefix}")
    await client.start_flow(flow, flow_id, input, StartFlowOptions())
    return flow_id


async def wait_for_completed_flow(client: AsyncClient, flow_id: str) -> FlowResult:
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.COMPLETED, result.error_message
    return result


def outputs_of(
    result: FlowResult, step_type: str, output_type: type[OutputT]
) -> list[OutputT]:
    return [
        completion.decode(output_type)
        for completion in result.completions
        if completion.step_type == step_type
    ]


def step_types_of(result: FlowResult) -> list[str]:
    return sorted(completion.step_type for completion in result.completions)


async def wait_for_step(
    client: AsyncClient, flow_id: str, step_type: str, number: int = 1
) -> None:
    await client.wait_for_step_completion(
        flow_id,
        StepExecutionId(step_type, number),
        WaitForStepCompletionOptions(request_timeout=WAIT_TIMEOUT),
    )


async def assert_running(client: AsyncClient, flow_id: str) -> None:
    assert await flow_status_or_none(client, flow_id) is FlowStatus.RUNNING


async def wait_for_attribute_value(
    client: AsyncClient, flow_id: str, attribute: Attribute[Any], expected: object
) -> None:
    async def has_expected_value() -> bool:
        values = await client.get_attributes(flow_id, attribute)
        return bool(values.get(attribute) == expected)

    await wait_until(f"{attribute.name} to become {expected}", has_expected_value)


async def retry_on_lock_conflict(invoke: Callable[[], Awaitable[OutputT]]) -> OutputT:
    """Retries an RPC that failed fast because a Step held the same lock."""
    deadline = asyncio.get_running_loop().time() + WAIT_TIMEOUT.total_seconds()
    while True:
        try:
            return await invoke()
        except RpcLockConflictError:
            if asyncio.get_running_loop().time() >= deadline:
                raise
            await asyncio.sleep(LOCK_CONFLICT_RETRY_SECONDS)


def assert_no_overlap(intervals: list[TaskInterval]) -> None:
    ordered = sorted(intervals, key=lambda interval: interval.started_at_millis)
    for earlier, later in zip(ordered, ordered[1:]):
        assert earlier.finished_at_millis <= later.started_at_millis, (
            f"{earlier.task} overlaps {later.task}"
        )


async def test_wcp1_sequence_runs_steps_one_after_another(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client, new_flow_id, patterns.sequence, "sequence", []
    )
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["ShipOrder"]
    assert outputs_of(result, "ShipOrder", list[str]) == [
        ["ReceiveOrder", "CheckInventory", "ShipOrder"]
    ]


async def test_wcp2_wcp3_parallel_split_and_synchronization_wait_for_every_branch(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.parallel_split_synchronization
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "parallel-split", None)
    await wait_for_step(client, flow_id, "PackItems")
    await assert_running(client, flow_id)
    await client.invoke_rpc(flow.approve_payment, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["SynchronizeFulfillment"]
    assert outputs_of(result, "SynchronizeFulfillment", list[str]) == [
        ["ChargePayment", "PackItems"]
    ]


async def test_wcp4_wcp5_exclusive_choice_and_simple_merge_run_one_branch(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.exclusive_choice_simple_merge
    auto_flow_id = await start_pattern_flow(client, new_flow_id, flow, "exclusive-auto", 250)
    auto_result = await wait_for_completed_flow(client, auto_flow_id)
    assert outputs_of(auto_result, "CloseClaim", list[str]) == [
        ["RouteClaim", "AutoApprove", "CloseClaim"]
    ]
    manual_flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "exclusive-manual", 5000
    )
    manual_result = await wait_for_completed_flow(client, manual_flow_id)
    assert outputs_of(manual_result, "CloseClaim", list[str]) == [
        ["RouteClaim", "ManualReview", "CloseClaim"]
    ]


async def test_wcp6_wcp7_wcp37_synchronizing_merge_waits_only_for_chosen_branches(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client,
        new_flow_id,
        patterns.multi_choice_synchronizing_merge,
        "multi-choice",
        NotificationChoice(send_email=True, send_sms=False, send_push=True),
    )
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["SynchronizingMerge"]
    assert outputs_of(result, "SynchronizingMerge", list[str]) == [
        ["SendEmail", "SendPush"]
    ]


async def test_wcp8_multi_merge_runs_the_merged_step_once_per_arrival(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client, new_flow_id, patterns.multi_merge, "multi-merge", None
    )
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["RecordReview", "RecordReview"]
    assert sorted(outputs_of(result, "RecordReview", str)) == [
        "FinanceReview",
        "LegalReview",
    ]


async def test_wcp38_general_synchronizing_merge_waits_for_the_subflow_region(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client, new_flow_id, patterns.general_synchronizing_merge, "general-merge", None
    )
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["PublishDecision"]
    assert outputs_of(result, "PublishDecision", list[str]) == [
        ["ReviewProposal-1", "TranslateProposal-0", "TranslateProposal-1"]
    ]


async def test_wcp9_structured_discriminator_fires_on_the_first_quote(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.structured_partial_join
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "discriminator", 1)
    await client.invoke_rpc(flow.release_supplier_quote, flow_id, "A")
    await wait_for_step(client, flow_id, "PlaceOrder")
    await client.invoke_rpc(flow.release_supplier_quote, flow_id, "B")
    await client.invoke_rpc(flow.release_supplier_quote, flow_id, "C")
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "PlaceOrder", list[str]) == [["A"]]
    assert outputs_of(result, "ResetQuoteJoin", list[str]) == [["B", "C"]]


async def test_wcp30_structured_partial_join_fires_after_two_quotes(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.structured_partial_join
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "partial-join", 2)
    await client.invoke_rpc(flow.release_supplier_quote, flow_id, "A")
    await wait_for_step(client, flow_id, "QuoteFromSupplierA")
    await client.invoke_rpc(flow.release_supplier_quote, flow_id, "B")
    await wait_for_step(client, flow_id, "PlaceOrder")
    await client.invoke_rpc(flow.release_supplier_quote, flow_id, "C")
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "PlaceOrder", list[str]) == [["A", "B"]]
    assert outputs_of(result, "ResetQuoteJoin", list[str]) == [["C"]]


async def send_branch_signals(
    client: AsyncClient, patterns: WorkflowPatternFlows, flow_id: str, *branches: str
) -> None:
    for branch in branches:
        await client.invoke_rpc(
            patterns.blocking_partial_join.send_branch_signal, flow_id, branch
        )


async def test_wcp28_blocking_discriminator_holds_a_second_signal_until_reset(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client,
        new_flow_id,
        patterns.blocking_partial_join,
        "blocking-discriminator",
        BlockingJoinRound(required_signals=1, round=1),
    )
    await send_branch_signals(client, patterns, flow_id, "A")
    await wait_for_step(client, flow_id, "ProcessRound")
    await send_branch_signals(client, patterns, flow_id, "A", "B", "C")
    await wait_for_step(client, flow_id, "ProcessRound", 2)
    await send_branch_signals(client, patterns, flow_id, "B", "C")
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "ProcessRound", ProcessedRound) == [
        ProcessedRound(1, ["A"]),
        ProcessedRound(2, ["A"]),
    ]


async def test_wcp31_blocking_partial_join_holds_second_round_signals_until_reset(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client,
        new_flow_id,
        patterns.blocking_partial_join,
        "blocking-partial-join",
        BlockingJoinRound(required_signals=2, round=1),
    )
    await send_branch_signals(client, patterns, flow_id, "A", "B")
    await wait_for_step(client, flow_id, "ProcessRound")
    await send_branch_signals(client, patterns, flow_id, "A", "B", "C")
    await wait_for_step(client, flow_id, "ProcessRound", 2)
    await send_branch_signals(client, patterns, flow_id, "C")
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "ProcessRound", ProcessedRound) == [
        ProcessedRound(1, ["A", "B"]),
        ProcessedRound(2, ["A", "B"]),
    ]


async def test_wcp29_cancelling_discriminator_cancels_the_other_carriers(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.cancelling_partial_join
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "cancelling-discriminator", 1
    )
    await client.invoke_rpc(flow.release_carrier_bid, flow_id, "B")
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["AcceptBids"]
    assert outputs_of(result, "AcceptBids", list[str]) == [["B"]]


async def test_wcp32_cancelling_partial_join_cancels_the_last_carrier(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.cancelling_partial_join
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "cancelling-partial-join", 2
    )
    await client.invoke_rpc(flow.release_carrier_bid, flow_id, "A")
    await wait_for_step(client, flow_id, "BidFromCarrierA")
    await client.invoke_rpc(flow.release_carrier_bid, flow_id, "C")
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["AcceptBids"]
    assert outputs_of(result, "AcceptBids", list[str]) == [["A", "C"]]


async def test_wcp33_generalized_and_join_keeps_an_extra_invoice(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.generalized_and_join
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "and-join", InvoicePaymentMatching(2, 0)
    )
    await client.invoke_rpc(flow.deliver_invoice, flow_id, "I1")
    await client.invoke_rpc(flow.deliver_invoice, flow_id, "I2")
    await client.invoke_rpc(flow.deliver_payment, flow_id, "P1")
    await wait_for_step(client, flow_id, "RecordMatch")
    await assert_running(client, flow_id)
    await client.invoke_rpc(flow.deliver_payment, flow_id, "P2")
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "RecordMatch", InvoicePaymentMatch) == [
        InvoicePaymentMatch("I1", "P1"),
        InvoicePaymentMatch("I2", "P2"),
    ]


async def test_wcp41_wcp42_thread_split_and_merge(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client, new_flow_id, patterns.thread_split_merge, "threads", 3
    )
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["MergeParcels"]
    assert outputs_of(result, "MergeParcels", list[int]) == [[0, 1, 2]]


async def test_wcp12_multiple_instances_without_synchronization(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.multiple_instances_without_synchronization
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "instances-unsynchronized", ["ana", "ben"]
    )
    await wait_for_step(client, flow_id, "ConfirmRegistration")
    await assert_running(client, flow_id)
    await client.invoke_rpc(flow.release_invitations, flow_id, 2)
    result = await wait_for_completed_flow(client, flow_id)
    assert sorted(outputs_of(result, "SendInvitation", str)) == ["ana", "ben"]
    assert outputs_of(result, "ConfirmRegistration", str) == ["ConfirmRegistration"]


async def test_wcp13_multiple_instances_with_design_time_knowledge(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client, new_flow_id, patterns.design_time_multiple_instances, "instances-design", None
    )
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "CollectReviews", list[str]) == [
        ["finance", "legal", "security"]
    ]


async def test_wcp14_multiple_instances_with_run_time_knowledge(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client,
        new_flow_id,
        patterns.run_time_multiple_instances,
        "instances-runtime",
        ["dee", "ana", "cy", "ben"],
    )
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "CollectReviews", list[str]) == [
        ["ana", "ben", "cy", "dee"]
    ]


async def test_wcp15_multiple_instances_without_a_priori_run_time_knowledge(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.no_prior_knowledge_multiple_instances
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "instances-open", [])
    for sample_id in ("s1", "s2", "s3"):
        assert await client.invoke_rpc(flow.add_sample, flow_id, sample_id) is True
    await client.invoke_rpc(flow.close_sample_intake, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "ReportSamples", list[str]) == [["s1", "s2", "s3"]]


async def test_wcp34_static_partial_join_for_multiple_instances(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.static_partial_join_for_multiple_instances
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "instances-static-join", PartialInspection(3, 2)
    )
    await client.invoke_rpc(flow.release_inspections, flow_id, 2)
    await wait_for_step(client, flow_id, "ApproveBatch")
    await assert_running(client, flow_id)
    await client.invoke_rpc(flow.release_inspections, flow_id, 1)
    result = await wait_for_completed_flow(client, flow_id)
    approved = outputs_of(result, "ApproveBatch", list[int])[0]
    reset = outputs_of(result, "ResetInspectionJoin", list[int])[0]
    assert len(approved) == 2
    assert len(reset) == 1
    assert sorted([*approved, *reset]) == [0, 1, 2]


async def test_wcp35_cancelling_partial_join_for_multiple_instances(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.cancelling_partial_join_for_multiple_instances
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "instances-cancelling-join", PartialInspection(3, 2)
    )
    await client.invoke_rpc(flow.release_quorum_inspections, flow_id, 2)
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["AcceptQuorumInspections"]
    assert len(set(outputs_of(result, "AcceptQuorumInspections", list[int])[0])) == 2


async def test_wcp36_dynamic_partial_join_for_multiple_instances(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.dynamic_partial_join_for_multiple_instances
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "instances-dynamic-join", BidJoin(2, [])
    )
    for amount in (500, 1500, 700):

        async def add_bid(bid_amount: int = amount) -> bool:
            return await client.invoke_rpc(flow.add_bid, flow_id, bid_amount)

        assert await retry_on_lock_conflict(add_bid) is True
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "AwardContract", list[int]) == [[500, 700]]


async def test_wcp16_deferred_choice_runs_only_the_first_response_branch(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.deferred_choice
    declined_flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "deferred-decline", None
    )
    await client.invoke_rpc(flow.decline_offer, declined_flow_id)
    declined = await wait_for_completed_flow(client, declined_flow_id)
    assert step_types_of(declined) == ["ArchiveOffer"]
    accepted_flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "deferred-accept", None
    )
    await client.invoke_rpc(flow.accept_offer, accepted_flow_id)
    accepted = await wait_for_completed_flow(client, accepted_flow_id)
    assert step_types_of(accepted) == ["FulfillOffer"]


async def test_wcp17_interleaved_parallel_routing_never_overlaps_and_keeps_order(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client, new_flow_id, patterns.interleaved_parallel_routing, "interleaved-parallel", None
    )
    result = await wait_for_completed_flow(client, flow_id)
    intervals = outputs_of(result, "CompleteOnboardingTasks", list[TaskInterval])[0]
    tasks = {interval.task: interval for interval in intervals}
    assert sorted(tasks) == ["PrepareContract", "RunCreditCheck", "SignContract"]
    assert_no_overlap(intervals)
    assert (
        tasks["PrepareContract"].finished_at_millis
        <= tasks["SignContract"].started_at_millis
    )


async def test_wcp18_milestone_accepts_a_change_only_while_the_window_is_open(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.milestone
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "milestone", None)
    await wait_for_attribute_value(client, flow_id, IS_CHANGE_WINDOW_OPEN, True)
    assert await client.invoke_rpc(flow.request_config_change, flow_id, "raise-limit") is True
    await client.invoke_rpc(flow.approve_release, flow_id)
    await wait_for_attribute_value(client, flow_id, IS_CHANGE_WINDOW_OPEN, False)
    assert await client.invoke_rpc(flow.request_config_change, flow_id, "late-change") is False
    await client.invoke_rpc(flow.finalize_release, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "ApplyConfigChange", str) == ["raise-limit"]


async def test_wcp39_critical_section_never_overlaps_the_two_sections(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client, new_flow_id, patterns.critical_section, "critical-section", None
    )
    result = await wait_for_completed_flow(client, flow_id)
    intervals = outputs_of(result, "FinishCriticalSections", list[TaskInterval])[0]
    assert sorted(interval.task for interval in intervals) == ["inventory", "pricing"]
    assert_no_overlap(intervals)


async def test_wcp40_interleaved_routing_runs_each_audit_alone(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client, new_flow_id, patterns.interleaved_routing, "interleaved", None
    )
    result = await wait_for_completed_flow(client, flow_id)
    intervals = outputs_of(result, "CompleteAudits", list[TaskInterval])[0]
    assert sorted(interval.task for interval in intervals) == [
        "AuditExpenses",
        "AuditPayroll",
        "AuditTaxes",
    ]
    assert_no_overlap(intervals)


async def test_wcp19_cancel_task_withdraws_the_waiting_pickup(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.cancel_task
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "cancel-task", None)
    await client.invoke_rpc(flow.cancel_courier_pickup, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["ArrangeSelfDelivery"]


async def test_wcp20_cancel_case_closes_the_flow_as_canceled(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client, new_flow_id, patterns.cancel_case, "cancel-case", None
    )
    await client.stop_flow(
        flow_id, StopFlowOptions(type=StopType.CANCEL, reason="applicant withdrew")
    )
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.CANCELED
    assert result.completions == ()


async def test_wcp25_cancel_region_withdraws_two_checks_and_keeps_the_third(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.cancel_region
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "cancel-region", None)
    await wait_for_step(client, flow_id, "StartApplicationChecks")
    await client.invoke_rpc(flow.reject_application, flow_id)
    await wait_for_step(client, flow_id, "NotifyRejection")
    await assert_running(client, flow_id)
    await client.invoke_rpc(flow.complete_sanctions_screening, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["NotifyRejection", "ScreenSanctions"]


async def test_wcp26_cancel_multiple_instance_activity_keeps_the_finished_one(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.cancel_multiple_instance_task
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "cancel-instances", 3)
    await wait_for_step(client, flow_id, "StartTranslations")
    await client.invoke_rpc(flow.release_translation, flow_id)
    await wait_for_attribute_value(client, flow_id, FINISHED_TRANSLATION_COUNT, 1)
    await client.invoke_rpc(flow.cancel_translations, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert len(outputs_of(result, "TranslateDocument", int)) == 1
    assert outputs_of(result, "ReportTranslations", int) == [1]


async def test_wcp27_complete_multiple_instance_activity_continues_early(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.complete_multiple_instance_task
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "complete-instances", 3)
    await wait_for_step(client, flow_id, "StartPeerReviews")
    await client.invoke_rpc(flow.release_peer_review, flow_id)

    async def has_one_submitted_review() -> bool:
        count = await client.invoke_rpc(flow.get_submitted_peer_review_count, flow_id)
        return bool(count == 1)

    await wait_until("the first peer review to be submitted", has_one_submitted_review)
    await client.invoke_rpc(flow.complete_peer_reviews_early, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["PublishReviewResults"]
    assert len(outputs_of(result, "PublishReviewResults", list[int])[0]) == 1


async def test_wcp10_arbitrary_cycles_enter_the_cycle_at_either_step(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.arbitrary_cycles
    new_article_flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "cycles-new", ArticleSubmission(has_draft=False)
    )
    new_article = await wait_for_completed_flow(client, new_article_flow_id)
    assert outputs_of(new_article, "PublishArticle", list[str]) == [
        [
            "RouteSubmission",
            "DraftArticle",
            "EditArticle",
            "DraftArticle",
            "EditArticle",
            "PublishArticle",
        ]
    ]
    drafted_flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "cycles-draft", ArticleSubmission(has_draft=True)
    )
    drafted = await wait_for_completed_flow(client, drafted_flow_id)
    assert outputs_of(drafted, "PublishArticle", list[str]) == [
        ["RouteSubmission", "EditArticle", "DraftArticle", "EditArticle", "PublishArticle"]
    ]


async def test_wcp21_structured_loop_tests_its_condition_before_each_pass(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.structured_loop
    three_flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "loop-three", BatchProgress(3, 0)
    )
    three = await wait_for_completed_flow(client, three_flow_id)
    assert outputs_of(three, "FinishBatches", int) == [3]
    zero_flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "loop-zero", BatchProgress(0, 0)
    )
    zero = await wait_for_completed_flow(client, zero_flow_id)
    assert outputs_of(zero, "FinishBatches", int) == [0]


async def test_wcp22_recursion_computes_a_factorial_through_subflows(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = await start_pattern_flow(
        client, new_flow_id, patterns.factorial_recursion, "recursion", 4
    )
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "ComputeFactorial", int) == [24]


async def test_wcp11_implicit_termination_completes_after_the_last_step(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.implicit_termination
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "implicit-termination", None
    )
    await wait_for_step(client, flow_id, "CreateAccount")
    await wait_for_step(client, flow_id, "SendWelcomeEmail")
    await assert_running(client, flow_id)
    await client.invoke_rpc(flow.complete_profile, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == [
        "AwaitProfileCompletion",
        "CreateAccount",
        "SendWelcomeEmail",
    ]


async def test_wcp43_explicit_termination_completes_at_once(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.explicit_termination
    flow_id = await start_pattern_flow(
        client, new_flow_id, flow, "explicit-termination", None
    )
    await wait_for_step(client, flow_id, "StartIncident")
    await client.invoke_rpc(flow.resolve_incident, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert step_types_of(result) == ["ResolveIncident"]


async def test_wcp23_transient_trigger_drops_an_event_no_step_waits_for(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.transient_trigger
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "transient-trigger", None)
    assert await client.invoke_rpc(flow.send_motion_event, flow_id, 1) is False
    await client.invoke_rpc(flow.arm_motion_sensor, flow_id)
    await wait_for_attribute_value(client, flow_id, IS_AWAITING_MOTION_EVENT, True)
    assert await client.invoke_rpc(flow.send_motion_event, flow_id, 2) is True
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "AwaitMotionEvent", int) == [2]


async def test_wcp24_persistent_trigger_keeps_a_label_sent_early(
    patterns: WorkflowPatternFlows,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = patterns.persistent_trigger
    flow_id = await start_pattern_flow(client, new_flow_id, flow, "persistent-trigger", None)
    await client.invoke_rpc(flow.send_shipment_label, flow_id, "label-1")
    await client.invoke_rpc(flow.mark_carrier_ready, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "AwaitShipmentLabel", str) == ["label-1"]
