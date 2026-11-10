# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""ConnectorToolSource without an engine: definitions, mapping, approval, and the fence context."""

from __future__ import annotations

import copy
import json
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any, cast

import pytest
from dex import AsyncContext
from dex_connectors.sdk import Call, CallID, ConnectionRef, OperationRef, derive_call_id
from dex_connectors.sdk.singledispatch import DecisionKind, Fence, Marker, State

from dex_agents import (
    AgentEventWriter,
    AgentToolCall,
    AgentTurnContext,
    ToolCallStatus,
    ToolEffect,
    ToolPolicy,
)
from dex_agents.connectors import (
    ConnectorBranchOutcome,
    ConnectorStepContext,
    ConnectorToolSource,
    bound_connector_result,
    classify_connector_branch,
    connector_tool,
    format_connector_result,
)
from dex_agents.connectors._schema import check_input_schema, find_input_violation
from dex_agents.connectors._source import (
    _is_proven_not_sent,
    _lower_camel_to_snake,
    _may_have_dispatched,
)
from tests.connector_agent import (
    CONNECTION,
    PREVIOUS_BY_ID_TOOL_NAME,
    by_id_manifest,
    read_fake_ledger_manifest,
)
from tests.fake_ledger import (
    CloseAccountOperation,
    CreatePayoutOperation,
    FakeLedgerProvider,
    GetBalanceByIDOperation,
    GetBalanceOperation,
    PostNoteOperation,
)

CONFORMANCE = json.loads(
    (Path(__file__).resolve().parents[3] / "conformance" / "connector_tools.json").read_text()
)


@pytest.fixture
def provider(tmp_path: Path) -> FakeLedgerProvider:
    return FakeLedgerProvider(tmp_path)


# Shared conformance cases


@pytest.mark.parametrize("case", CONFORMANCE["argumentCases"])
def test_arguments_are_checked_as_the_conformance_suite_says(case: dict[str, Any]) -> None:
    assert find_input_violation(case["arguments"], CONFORMANCE["inputSchema"]) == case["violation"]


@pytest.mark.parametrize("case", CONFORMANCE["refusedSchemas"])
def test_a_schema_with_an_unchecked_keyword_is_refused(case: dict[str, Any]) -> None:
    with pytest.raises(ValueError) as raised:
        check_input_schema(case["schema"])
    assert str(raised.value) == case["error"]


def test_the_conformance_schema_is_accepted() -> None:
    check_input_schema(CONFORMANCE["inputSchema"])


@pytest.mark.parametrize("case", CONFORMANCE["branchCases"])
def test_branches_map_as_the_conformance_suite_says(case: dict[str, Any]) -> None:
    outcome = classify_connector_branch(
        kind=case["kind"],
        branch=case["branch"],
        required_branches=frozenset(case["requiredBranches"]),
        agent_branches=case["agentBranches"],
        may_have_dispatched=case.get("mayHaveDispatched", False),
    )
    assert outcome is ConnectorBranchOutcome(case["outcome"])


@pytest.mark.parametrize("case", CONFORMANCE["toolNameCases"])
def test_operation_ids_become_snake_case_as_the_conformance_suite_says(
    case: dict[str, Any],
) -> None:
    assert _lower_camel_to_snake(case["operationId"]) == case["snakeName"]


@pytest.mark.parametrize(
    "case", CONFORMANCE["fenceObservationCases"], ids=lambda case: str(case["name"])
)
def test_fence_events_are_observed_as_the_conformance_suite_says(case: dict[str, Any]) -> None:
    operation = OperationRef("fake-ledger", "postNote")
    call_id = str(derive_call_id("session-1", "CallTool-3", operation))
    other_call_id = str(derive_call_id("session-1", "CallTool-4", operation))

    def bind(value: str) -> str:
        return value.replace("<callId>", call_id).replace("<otherCallId>", other_call_id)

    previous = case["previousCheckpoint"]
    call = _fenced_call(
        case["attempt"], None if previous is None else bind(previous), _ToolStepHeartbeats()
    )
    context = cast(ConnectorStepContext, call.context)
    if case["readsCheckpoint"]:
        context.has_last_heartbeat_value()
    for saved in case["savedHeartbeats"]:
        bound = json.loads(bind(json.dumps(saved)))
        try:
            value: object = Marker.from_wire(bound)
        except ValueError:
            value = bound
        # The fence's markers, or the operation's own value, as heartbeat() stores them.
        context._saved_heartbeat_values.append(value)
    assert _may_have_dispatched(call) is case["mayHaveDispatched"]
    assert _is_proven_not_sent(call) is case["isProvenNotSent"]


@pytest.mark.parametrize("case", CONFORMANCE["resultCases"])
def test_result_text_is_the_same_in_every_core(case: dict[str, Any]) -> None:
    assert format_connector_result(case["json"]) == case["text"]


@pytest.mark.parametrize("case", CONFORMANCE["truncationCases"])
def test_results_are_truncated_as_the_conformance_suite_says(case: dict[str, Any]) -> None:
    result = bound_connector_result(case["output"], case["maxResultBytes"])
    assert result.output == case["expectedOutput"]
    assert result.truncated_output_bytes == case["truncatedOutputBytes"]


# Definitions from the manifest


def test_each_operation_kind_maps_to_its_effect_class_and_step_options(
    provider: FakeLedgerProvider,
) -> None:
    query = connector_tool(GetBalanceOperation(provider), CONNECTION)
    safe_write = connector_tool(CreatePayoutOperation(provider), CONNECTION)
    single_write = connector_tool(PostNoteOperation(provider), CONNECTION)
    assert (query.name, query.effect) == ("fake-ledger_get_balance", ToolEffect.READ)
    assert (safe_write.name, safe_write.effect) == (
        "fake-ledger_create_payout",
        ToolEffect.IDEMPOTENT_WRITE,
    )
    assert single_write.effect is ToolEffect.NON_IDEMPOTENT_WRITE
    assert query.description == "Read the balance of one ledger account."
    assert safe_write.is_approval_recommended and not single_write.is_approval_recommended
    for tool in (query, safe_write, single_write):
        assert tool.execute_method_timeout == timedelta(seconds=30)
        assert tool.retry_policy is not None
        # The connector policy's one-hour window, narrowed to the tool Steps' three attempts.
        assert tool.retry_policy.maximum_attempts == 3
        assert tool.retry_policy.total_duration == timedelta(hours=1)


def test_an_explicit_name_keeps_a_previous_default_tool_name(
    provider: FakeLedgerProvider,
) -> None:
    operation = GetBalanceByIDOperation(provider)
    manifest = by_id_manifest()
    default = connector_tool(operation, CONNECTION, manifest=manifest)
    assert default.name == "fake-ledger_get_balance_by_id"
    kept = connector_tool(operation, CONNECTION, manifest=manifest, name=PREVIOUS_BY_ID_TOOL_NAME)
    assert kept.name == "fake-ledger_get_balance_by_i_d"


def test_an_operation_without_agent_exposed_is_refused(provider: FakeLedgerProvider) -> None:
    with pytest.raises(ValueError, match="closeAccount is not exposed to agents"):
        connector_tool(CloseAccountOperation(), CONNECTION)
    manifest = copy.deepcopy(read_fake_ledger_manifest())
    manifest["factories"][0]["agent"]["exposed"] = False
    with pytest.raises(ValueError, match="getBalance is not exposed to agents"):
        connector_tool(GetBalanceOperation(provider), CONNECTION, manifest=manifest)


@pytest.mark.parametrize(
    ("change", "message"),
    [
        (lambda factory: factory["agent"].update(approval="always"), "unknown agent.approval"),
        (lambda factory: factory["agent"].update(retry="maybe"), "unknown agent.retry"),
        (
            lambda factory: factory["agent"].update(branches={"notFound": "fine"}),
            "agent.branches values must be rejected or unknown",
        ),
        (lambda factory: factory.update(kind="query"), "the manifest says query"),
        (
            lambda factory: factory["agent"].update(branches={"paid": "rejected"}),
            "may list only optional branches",
        ),
        (
            lambda factory: factory["inputSchema"]["properties"]["accountId"].update(pattern="x"),
            "uses pattern",
        ),
    ],
)
def test_a_manifest_entry_the_core_cannot_honour_is_refused(
    provider: FakeLedgerProvider, change: Any, message: str
) -> None:
    manifest = copy.deepcopy(read_fake_ledger_manifest())
    change(manifest["factories"][1])
    with pytest.raises(ValueError, match=message):
        connector_tool(CreatePayoutOperation(provider), CONNECTION, manifest=manifest)


def test_a_query_cannot_list_an_unknown_branch(provider: FakeLedgerProvider) -> None:
    manifest = copy.deepcopy(read_fake_ledger_manifest())
    manifest["factories"][0]["agent"]["branches"] = {"invalidResponse": "unknown"}
    with pytest.raises(ValueError, match="unknown is for Mutations only"):
        connector_tool(GetBalanceOperation(provider), CONNECTION, manifest=manifest)


def test_two_tools_with_one_name_are_refused(provider: FakeLedgerProvider) -> None:
    tool = connector_tool(GetBalanceOperation(provider), CONNECTION)
    with pytest.raises(ValueError, match="share a name"):
        ConnectorToolSource([tool, tool])


# Approval


def _turn(tool_policies: dict[str, ToolPolicy] | None = None) -> AgentTurnContext:
    async def heartbeat() -> None:
        return None

    return AgentTurnContext(
        flow_id="session-1",
        turn_id="turn-1",
        attempt=1,
        messages=(),
        framework_run_state=None,
        tool_calls={},
        tool_policies=tool_policies or {},
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=cast(AgentEventWriter, None),
        heartbeat=heartbeat,
    )


def _approval(
    source: ConnectorToolSource, name: str, arguments: dict[str, Any], **turn: Any
) -> bool:
    call = source.deferred_tool_call(
        _turn(**turn),
        tool_call_id="call-1",
        tool_name=name,
        arguments=json.dumps(arguments),
        listed_tool=None,
    )
    return call.policy.is_approval_required


def test_approval_follows_the_manifest_unless_the_application_or_session_overrides_it(
    provider: FakeLedgerProvider,
) -> None:
    payouts = CreatePayoutOperation(provider)

    def predicate(arguments: Any) -> bool:
        return bool(arguments["amount"] > 50)

    def broken(arguments: Any) -> bool:
        raise RuntimeError("no limit configured")

    source = ConnectorToolSource(
        [
            connector_tool(payouts, CONNECTION, name="recommended"),
            connector_tool(payouts, CONNECTION, name="never", requires_approval=False),
            connector_tool(PostNoteOperation(provider), CONNECTION, name="note"),
            connector_tool(payouts, CONNECTION, name="limited", requires_approval=predicate),
            connector_tool(payouts, CONNECTION, name="broken", requires_approval=broken),
        ]
    )
    payout = {"accountId": "a", "amount": 10}
    assert _approval(source, "recommended", payout)
    assert not _approval(source, "never", payout)
    # Mutations run without approval by default, though NON_IDEMPOTENT_WRITE asks by default.
    assert not _approval(source, "note", {"accountId": "a", "text": "hi"})
    assert not _approval(source, "limited", payout)
    assert _approval(source, "limited", {"accountId": "a", "amount": 51})
    # Arguments the tool Step refuses never run, so they never wait.
    assert not _approval(source, "limited", {"accountId": "a"})
    assert _approval(source, "broken", payout)
    assert not _approval(
        source,
        "recommended",
        payout,
        tool_policies={"recommended": ToolPolicy(ToolEffect.READ, requires_approval=False)},
    )


def test_a_session_policy_never_changes_the_effect_class(provider: FakeLedgerProvider) -> None:
    source = ConnectorToolSource([connector_tool(PostNoteOperation(provider), CONNECTION)])
    call = source.deferred_tool_call(
        _turn({"fake-ledger_post_note": ToolPolicy(ToolEffect.READ)}),
        tool_call_id="call-1",
        tool_name="fake-ledger_post_note",
        arguments="{}",
        listed_tool=None,
    )
    assert call.policy.effect is ToolEffect.NON_IDEMPOTENT_WRITE
    assert call.ordered
    record = AgentToolCall(
        tool_call_id="call-1",
        turn_id="turn-1",
        tool_name="fake-ledger_post_note",
        arguments="{}",
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=False,
        status=ToolCallStatus.APPROVED,
        tool_source_reference=call.tool_source_reference,
    )
    options = source.tool_call_step_options(record)
    assert options is not None and options.execute_method_timeout == timedelta(seconds=30)


# The Step context the single-dispatch fence uses


class _LiveContext:
    """The members of AsyncContext that ConnectorStepContext reads."""

    def __init__(self, attempt: int) -> None:
        self.flow_id = "session-1"
        self.run_id = "run-1"
        self.step_execution_id = "CallTool-3"
        self.attempt = attempt
        self.deadline = datetime.now(UTC) + timedelta(minutes=1)


class _ToolStepHeartbeats:
    """The session's checkpoint behavior: each heartbeat repeats the latest text checkpoint."""

    def __init__(self) -> None:
        self.recorded: list[str | None] = []
        self.checkpoint: str | None = None

    async def record(self) -> None:
        self.recorded.append(self.checkpoint)

    async def save(self, checkpoint: str) -> None:
        self.checkpoint = checkpoint
        await self.record()


def _fenced_call(attempt: int, previous: str | None, heartbeats: _ToolStepHeartbeats) -> Call:
    operation = OperationRef("fake-ledger", "postNote")
    context = ConnectorStepContext(
        cast(AsyncContext, _LiveContext(attempt)),
        previous_checkpoint=previous,
        heartbeat=heartbeats.record,
        save_checkpoint=heartbeats.save,
    )
    return Call(
        derive_call_id("session-1", "CallTool-3", operation),
        ConnectionRef("fake-ledger", "test-ledger"),
        operation,
        context=cast(AsyncContext, context),
    )


def test_the_step_context_exposes_only_what_the_fence_reads() -> None:
    public = {name for name in dir(ConnectorStepContext) if not name.startswith("_")}
    assert public == {
        "attempt",
        "deadline",
        "flow_id",
        "get_last_heartbeat_value",
        "has_last_heartbeat_value",
        "heartbeat",
        "run_id",
        "step_execution_id",
    }


async def test_every_fence_method_keeps_its_marker_through_the_session_checkpoint() -> None:
    first = _ToolStepHeartbeats()
    fence = Fence(_fenced_call(1, None, first), timedelta(seconds=30))
    assert (await fence.decide()).kind is DecisionKind.SEND_ALLOWED

    async def send(deadline: datetime) -> str:
        # A bare background heartbeat now repeats the claim instead of clearing it.
        await first.record()
        return "note-1"

    assert await fence.dispatch(send) == "note-1"
    await fence.record_provider_id("note-1")
    assert first.checkpoint is not None
    assert all(value is not None for value in first.recorded)

    second = _ToolStepHeartbeats()
    retry = Fence(_fenced_call(2, first.checkpoint, second), timedelta(seconds=30))
    decision = await retry.decide()
    assert decision.kind is DecisionKind.READ_BACK_REQUIRED
    assert decision.marker is not None and decision.marker.provider_id == "note-1"


async def test_a_marker_proving_no_send_lets_the_next_attempt_send() -> None:
    first = _ToolStepHeartbeats()
    fence = Fence(_fenced_call(1, None, first), timedelta(seconds=30))
    await fence.decide()
    await fence.record_not_sent()
    retry = Fence(_fenced_call(2, first.checkpoint, _ToolStepHeartbeats()), timedelta(seconds=30))
    decision = await retry.decide()
    assert decision.kind is DecisionKind.SEND_ALLOWED
    assert decision.marker is not None and decision.marker.state is State.NOT_SENT

    resent = _ToolStepHeartbeats()
    fence = Fence(_fenced_call(1, None, resent), timedelta(seconds=30))
    await fence.decide()

    async def rejected_send(deadline: datetime) -> None:
        return None

    await fence.dispatch(rejected_send)
    await fence.record_can_resend()
    decision = await Fence(
        _fenced_call(2, resent.checkpoint, _ToolStepHeartbeats()), timedelta(seconds=30)
    ).decide()
    assert decision.kind is DecisionKind.SEND_ALLOWED


async def test_a_checkpoint_that_is_not_a_marker_fails_closed() -> None:
    fence = Fence(_fenced_call(2, '"a task handle"', _ToolStepHeartbeats()), timedelta(seconds=30))
    assert (await fence.decide()).kind is DecisionKind.FAIL_CLOSED


async def test_only_a_saved_not_sent_or_can_resend_marker_of_this_attempt_proves_no_send() -> None:
    no_marker = _fenced_call(1, None, _ToolStepHeartbeats())
    assert not _is_proven_not_sent(no_marker)

    not_sent = _fenced_call(1, None, _ToolStepHeartbeats())
    fence = Fence(not_sent, timedelta(seconds=30))
    await fence.decide()
    await fence.record_not_sent()
    assert _is_proven_not_sent(not_sent)

    claimed = _fenced_call(1, None, _ToolStepHeartbeats())
    fence = Fence(claimed, timedelta(seconds=30))
    await fence.decide()

    async def send(deadline: datetime) -> str:
        return "note-1"

    await fence.dispatch(send)
    assert not _is_proven_not_sent(claimed)
    await fence.record_can_resend()
    assert _is_proven_not_sent(claimed)

    accepted = _fenced_call(1, None, _ToolStepHeartbeats())
    fence = Fence(accepted, timedelta(seconds=30))
    await fence.decide()
    await fence.dispatch(send)
    await fence.record_provider_id("note-1")
    assert not _is_proven_not_sent(accepted)

    # A marker an earlier attempt saved is history, not proof about this attempt.
    retry_heartbeats = _ToolStepHeartbeats()
    first = _ToolStepHeartbeats()
    fence = Fence(_fenced_call(1, None, first), timedelta(seconds=30))
    await fence.decide()
    await fence.record_not_sent()
    assert not _is_proven_not_sent(_fenced_call(2, first.checkpoint, retry_heartbeats))


async def test_only_fence_checkpoints_proving_no_send_keep_the_manifest_listing() -> None:
    async def send(deadline: datetime) -> str:
        return "note-1"

    async def refused_send(deadline: datetime) -> None:
        return None

    unfenced = _fenced_call(2, None, _ToolStepHeartbeats())
    assert not _may_have_dispatched(unfenced)

    not_sent = _ToolStepHeartbeats()
    call = _fenced_call(1, None, not_sent)
    fence = Fence(call, timedelta(seconds=30))
    await fence.decide()
    assert not _may_have_dispatched(call)
    await fence.record_not_sent()
    assert not _may_have_dispatched(call)

    claimed = _ToolStepHeartbeats()
    call = _fenced_call(1, None, claimed)
    fence = Fence(call, timedelta(seconds=30))
    await fence.decide()
    await fence.dispatch(send)
    assert _may_have_dispatched(call)
    await fence.record_provider_id("note-1")
    assert _may_have_dispatched(call)

    can_resend = _ToolStepHeartbeats()
    call = _fenced_call(1, None, can_resend)
    fence = Fence(call, timedelta(seconds=30))
    await fence.decide()
    await fence.dispatch(refused_send)
    await fence.record_can_resend()
    assert not _may_have_dispatched(call)

    foreign = not_sent.checkpoint
    assert foreign is not None
    foreign = foreign.replace(str(call.id), "f" * len(str(call.id)))
    previous_checkpoints = {
        "a notSent marker of the attempt before": (not_sent.checkpoint, False),
        "a canResend marker of the attempt before": (can_resend.checkpoint, False),
        "a claim": (claimed.checkpoint, True),
        "a marker of another call": (foreign, True),
        "a malformed marker": ('{"callId": 7, "state": "notSent"}', True),
        "a checkpoint that is not a marker": ('"a task handle"', True),
        "no checkpoint": (None, True),
    }
    for label, (previous, expected) in previous_checkpoints.items():
        retry = _fenced_call(2, previous, _ToolStepHeartbeats())
        await Fence(retry, timedelta(seconds=30)).decide()
        assert _may_have_dispatched(retry) is expected, label
    stale = _fenced_call(3, not_sent.checkpoint, _ToolStepHeartbeats())
    await Fence(stale, timedelta(seconds=30)).decide()
    assert _may_have_dispatched(stale)


class _FailingSaves(_ToolStepHeartbeats):
    async def save(self, checkpoint: str) -> None:
        raise RuntimeError("the heartbeat did not reach Dex")


async def test_a_marker_that_failed_to_save_proves_nothing() -> None:
    call = _fenced_call(1, None, _FailingSaves())
    fence = Fence(call, timedelta(seconds=30))
    await fence.decide()
    with pytest.raises(RuntimeError):
        await fence.record_not_sent()
    assert not _is_proven_not_sent(call)


def test_the_call_id_depends_only_on_the_flow_step_execution_and_operation() -> None:
    first = _fenced_call(1, None, _ToolStepHeartbeats())
    retry = _fenced_call(2, None, _ToolStepHeartbeats())
    assert first.id == retry.id
    assert first.id == CallID(
        derive_call_id("session-1", "CallTool-3", OperationRef("fake-ledger", "postNote"))
    )
