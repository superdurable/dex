# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The application's ModelSpendLimit gates each model call and hears what it used."""

from __future__ import annotations

from pathlib import Path

from dex_agents import (
    MODEL_CALL_REFUSED_ERROR_PREFIX,
    TURN_FAILURE_NOTE_HEADER,
    AgentEventType,
    AgentSessionStatus,
    MessageRole,
)
from dex_agents.builtin_loop import AgentDefinition, builtin_agent_session_options
from dex_agents.testing import ScriptedModelClient, ScriptedModelReply, wait_for_agent_snapshot
from tests.conftest import ScriptedAgentHarness
from tests.file_spend_limit import (
    CHECK_MODE_FILE,
    CHECKS_LOG,
    REPORT_MODE_FILE,
    REPORTS_LOG,
    FileSpendLimit,
    read_spend_log,
)
from tests.scripted_agent import (
    SPEND_LIMIT_ENVIRONMENT_VARIABLE,
    SPEND_LIMIT_TIMEOUT_ENVIRONMENT_VARIABLE,
)
from tests.test_compaction import (
    PAD_TURN_COMMANDS,
    CompactionHarness,
    compaction_harness,  # noqa: F401
)

SPEND_LIMIT_ON = {SPEND_LIMIT_ENVIRONMENT_VARIABLE: "1"}


def _set_mode(state_dir: Path, file_name: str, mode: str) -> None:
    (state_dir / file_name).write_text(mode)


def test_a_stepwise_definition_carries_the_spend_limit(tmp_path: Path) -> None:
    spend_limit = FileSpendLimit(tmp_path)
    definition = AgentDefinition(
        name="agent",
        instructions="Help.",
        model=ScriptedModelClient(lambda _: ScriptedModelReply(text="hi")),
        spend_limit=spend_limit,
    )

    assert builtin_agent_session_options(definition).spend_limit is spend_limit


async def test_every_call_is_checked_before_and_reported_after(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker(SPEND_LIMIT_ON)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call lookup {"query": "x"}', session_settings={"account": "acme"}
    )
    await harness.wait_for_turns(session_id, 1)

    checks = read_spend_log(harness.state_dir, CHECKS_LOG)
    reports = read_spend_log(harness.state_dir, REPORTS_LOG)
    assert [check["step_type"] for check in checks] == ["RunModel", "RunModel"]
    assert [check["session_settings"] for check in checks] == [{"account": "acme"}] * 2
    assert [check["session_usage"]["model_calls"] for check in checks] == [0, 1]
    assert len(reports) == 2
    assert len({report["report_id"] for report in reports}) == 2
    assert reports[0]["report_id"].startswith(f"{session_id}/turn-1/")
    assert reports[1]["session_usage"]["model_calls"] == 2


async def test_a_refused_call_ends_the_turn_with_the_reason_and_a_later_message_can_run(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker(SPEND_LIMIT_ON)
    _set_mode(harness.state_dir, CHECK_MODE_FILE, "refuse account over its monthly limit")
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hello")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert (
        snapshot.state.last_turn_error
        == f"{MODEL_CALL_REFUSED_ERROR_PREFIX}: account over its monthly limit"
    )
    assert harness.invocations.count("model:turn-1:1") == 0
    assert read_spend_log(harness.state_dir, REPORTS_LOG) == []
    event_types = [event.type for event in await harness.agent_events(session_id)]
    assert event_types.count(AgentEventType.TURN_FAILED.value) == 1
    assert (await harness.executed_step_types(session_id))[-2:] == ["RunModel", "RecordTurnFailure"]

    _set_mode(harness.state_dir, CHECK_MODE_FILE, "allow")
    assert await harness.sessions.send_user_message(session_id, "say again")
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == "said: again"


async def test_a_refusal_after_a_tool_call_keeps_the_result_for_the_next_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker(SPEND_LIMIT_ON)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call lookup {"query": "q", "gate": "read-gate"}'
    )
    gate = harness.gate("read-gate")
    await gate.wait_until_reached()
    _set_mode(harness.state_dir, CHECK_MODE_FILE, "refuse out of budget")
    gate.release()

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error == f"{MODEL_CALL_REFUSED_ERROR_PREFIX}: out of budget"
    assert snapshot.tool_calls == []
    assert TURN_FAILURE_NOTE_HEADER in (snapshot.messages[-1].text or "")
    assert snapshot.messages[-1].role is MessageRole.USER
    assert len(read_spend_log(harness.state_dir, REPORTS_LOG)) == 1


async def test_a_check_that_raises_fails_closed_after_the_retries(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker(SPEND_LIMIT_ON)
    _set_mode(harness.state_dir, CHECK_MODE_FILE, "raise")
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hello")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert "the spend service is down" in (snapshot.state.last_turn_error or "")
    assert not (snapshot.state.last_turn_error or "").startswith(MODEL_CALL_REFUSED_ERROR_PREFIX)
    assert [check["attempt"] for check in read_spend_log(harness.state_dir, CHECKS_LOG)] == [
        1,
        2,
        3,
    ]
    assert harness.invocations.count("model:turn-1:1") == 0


async def test_a_check_that_recovers_lets_the_retry_call_the_model(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker(SPEND_LIMIT_ON)
    _set_mode(harness.state_dir, CHECK_MODE_FILE, "raise-once")
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hello")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "said: hello"
    assert [check["attempt"] for check in read_spend_log(harness.state_dir, CHECKS_LOG)] == [1, 2]
    (report,) = read_spend_log(harness.state_dir, REPORTS_LOG)
    assert report["attempt"] == 2


async def test_a_check_that_hangs_times_out_and_fails_closed(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker(
        {**SPEND_LIMIT_ON, SPEND_LIMIT_TIMEOUT_ENVIRONMENT_VARIABLE: "1"}
    )
    _set_mode(harness.state_dir, CHECK_MODE_FILE, "hang")
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hello")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error
    assert len(read_spend_log(harness.state_dir, CHECKS_LOG)) == 3
    assert harness.invocations.count("model:turn-1:1") == 0


async def test_a_report_that_raises_does_not_repeat_the_paid_call(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker(SPEND_LIMIT_ON)
    _set_mode(harness.state_dir, REPORT_MODE_FILE, "raise")
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hello")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "said: hello"
    assert harness.invocations.count("model:turn-1:1") == 1
    assert len(read_spend_log(harness.state_dir, REPORTS_LOG)) == 1


async def test_a_failed_attempt_reports_nothing_and_its_retry_reports_once(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker(SPEND_LIMIT_ON)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "fail")

    await harness.wait_for_turns(session_id, 1)

    assert harness.invocations.count("model:turn-1:1") == 3
    assert len(read_spend_log(harness.state_dir, CHECKS_LOG)) == 3
    assert read_spend_log(harness.state_dir, REPORTS_LOG) == []


async def test_a_worker_killed_after_the_report_reports_the_retry_under_a_new_id(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker(SPEND_LIMIT_ON)
    _set_mode(harness.state_dir, REPORT_MODE_FILE, "gate after-report")
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hello")
    gate = harness.gate("after-report")
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await harness.stack.start_worker(SPEND_LIMIT_ON)
    snapshot = await harness.wait_for_turns(session_id, 1)

    reports = read_spend_log(harness.state_dir, REPORTS_LOG)
    assert [report["attempt"] for report in reports] == [1, 2]
    assert len({report["report_id"] for report in reports}) == 2
    assert harness.invocations.count("model:turn-1:1") == 2
    # Both calls were paid for, and only the committed one is in AgentUsage.
    assert snapshot.usage.model_calls == 1


async def test_compact_context_is_checked_and_reported_and_a_refusal_ends_the_turn(
    compaction_harness: CompactionHarness,  # noqa: F811
) -> None:
    harness = compaction_harness
    await harness.stack.start_worker(SPEND_LIMIT_ON)
    session_id = harness.new_session_id()
    await harness.run_turns(session_id, PAD_TURN_COMMANDS[:-1])
    assert "CompactContext" not in await harness.executed_step_types(session_id)

    assert await harness.sessions.send_user_message(session_id, PAD_TURN_COMMANDS[-1])
    await harness.wait_for_turns(session_id, len(PAD_TURN_COMMANDS))

    compact_reports = [
        report
        for report in read_spend_log(harness.state_dir, REPORTS_LOG)
        if report["step_type"] == "CompactContext"
    ]
    assert len(compact_reports) == 1
    assert compact_reports[0]["usage"]["model_calls"] >= 1
    checks = read_spend_log(harness.state_dir, CHECKS_LOG)
    assert [check["step_type"] for check in checks].count("CompactContext") == 1

    # A refused compaction ends the turn like a refused RunModel.
    refused_id = harness.new_session_id()
    await harness.run_turns(refused_id, PAD_TURN_COMMANDS[:-1])
    _set_mode(harness.state_dir, CHECK_MODE_FILE, "refuse compact out of budget")
    summaries_before = len(harness.model_requests("summary"))
    assert await harness.sessions.send_user_message(refused_id, PAD_TURN_COMMANDS[-1])
    snapshot = await wait_for_agent_snapshot(
        harness.sessions,
        refused_id,
        lambda current: current
        if current.state is not None and current.state.turn_count >= len(PAD_TURN_COMMANDS)
        else None,
        description="the refused turn",
    )
    assert snapshot.state is not None
    assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert snapshot.state.last_turn_error == (
        f"{MODEL_CALL_REFUSED_ERROR_PREFIX}: compact out of budget"
    )
    assert len(harness.model_requests("summary")) == summaries_before
