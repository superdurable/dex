# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted runner whose tools start remote tasks that ``poll_tool_task`` polls.

The model reads ``call <tool> <json>`` from the user message, joined with
`` && call `` for parallel calls. The second model call answers
``<tool>: <result>`` per call, joined with ``; ``. ``say <text>`` answers
``<text>`` without a tool call. ``report`` is a ``read``
tool and ``publish_report`` a ``non_idempotent_write`` tool without approval.

Both start a task stored as a JSON file in the state directory and return
``ToolTaskStarted``. The invocation log records ``start <call> attempt <n>``
for every tool run, ``create <call>`` for every new task, and
``poll <call> attempt <n> pending <answered|declined|deadline_passed|none>``
for every poll. Arguments:

* ``topic``: the report finishes as ``report on <topic>``.
* ``polls``: the poll that finishes the task, counted from 1. Defaults to 2.
* ``interval``: the poll interval in seconds. Defaults to 1.
* ``start_gate``: the tool saves the task ID as its heartbeat checkpoint,
  repeats it in heartbeats for 2 seconds, and waits at this ``FileGate``.
* ``poll_gate``: every poll marks this gate, and the task finishes only once
  it is released.
* ``block_gate``: every poll waits at this gate. With
  ``ignores_cancellation``, a poll keeps waiting when ``cancel_turn``
  cancels it, records ``poll <call> cancelled``, and once the gate is released
  records ``poll <call> returned`` and finishes the task.
* ``fail_cancel``: every ``cancel_tool_task`` raises. Every call records
  ``cancel-task <call> attempt <n> <status> <cancellation_reason>``.
* ``ask``: the first poll asks ``Which format?`` with the choices ``pdf`` and
  ``html``; the poll after the answer records ``deliver <call> <answer>``. A
  declined or expired question records ``cancel <call>`` and the poll raises,
  because the task cannot take a decline, unless ``takes_decline`` is set:
  then it records ``deliver <call> <resolution>`` and the task finishes as
  ``report on <topic> without a format (<resolution>)``.
* ``continuations``: every poll records ``continuation <call> <text|none>``
  for the ``input_continuation`` it reads, and an odd-numbered poll that does
  not finish returns the continuation ``pending-<poll>``.
* ``fail_polls``: every poll raises, as when the task's server is down.
* ``fail_task``: the task fails with ``the report failed``.

``TASK_AGENT_WITHOUT_POLLER`` set to ``1`` builds a runner without
``poll_tool_task``, and ``TASK_AGENT_INTERRUPT_DEADLINE_SECONDS`` sets the
question deadline.
"""

from __future__ import annotations

import asyncio
import json
import os
import uuid
from datetime import timedelta
from pathlib import Path
from typing import Any

from dex_agents import (
    AgentFrameworkRunner,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    DeferredToolCall,
    DeferredToolExecution,
    DeferredToolOutcome,
    FrameworkMessage,
    MessageRole,
    QuestionResolution,
    ToolCallsRequested,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolInputRequested,
    ToolPolicy,
    ToolTaskCancellation,
    ToolTaskPending,
    ToolTaskPoll,
    ToolTaskPollOutcome,
    ToolTaskStarted,
    TurnCompleted,
    describe_tool_call_outcome,
)
from dex_agents.testing import FileGate, InvocationLog

TASK_AGENT_FLOW_TYPE = "TaskScriptedAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "TASK_AGENT_STATE_DIR"
WITHOUT_POLLER_ENVIRONMENT_VARIABLE = "TASK_AGENT_WITHOUT_POLLER"
INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE = "TASK_AGENT_INTERRUPT_DEADLINE_SECONDS"
INVOCATION_LOG_NAME = "task-invocations.log"
TASK_QUESTION = "Which format?"
TOOL_POLICIES = {
    "report": ToolPolicy(ToolEffect.READ),
    "publish_report": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
}
_MODEL_CALL_USAGE = AgentUsage(model_calls=1, input_tokens=10, output_tokens=5)


class TaskStartingRunner(AgentFrameworkRunner[DeferredToolOutcome]):
    """Starts tasks but cannot poll them."""

    def __init__(self, state_dir: Path) -> None:
        self._state_dir = state_dir
        self._invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    def build_user_message(self, text: str) -> FrameworkMessage:
        return FrameworkMessage(
            role=MessageRole.USER, text=text, framework_item=json.dumps({"text": text})
        )

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        if turn.framework_run_state is not None:
            return _answer_with_results(turn, json.loads(turn.framework_run_state))
        command = json.loads(turn.messages[-1].framework_item)["text"]
        if command.startswith("say "):
            text = command.removeprefix("say ")
            return TurnCompleted(
                new_messages=[
                    FrameworkMessage(
                        role=MessageRole.ASSISTANT,
                        text=text,
                        framework_item=json.dumps({"text": text}),
                    )
                ],
                usage=_MODEL_CALL_USAGE,
            )
        calls = []
        for index, request in enumerate(command.removeprefix("call ").split(" && call ")):
            tool_name, arguments = request.split(" ", 1)
            calls.append([f"{turn.turn_id}-call-{index}", tool_name, arguments])
        return ToolCallsRequested(
            framework_run_state=json.dumps(calls),
            tool_calls=[
                DeferredToolCall(
                    tool_call_id=tool_call_id,
                    tool_name=tool_name,
                    arguments=arguments,
                    policy=turn.resolve_tool_policy(tool_name),
                )
                for tool_call_id, tool_name, arguments in calls
            ],
            usage=_MODEL_CALL_USAGE,
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        record = call.tool_call
        arguments = json.loads(record.arguments)
        self._invocations.record(f"start {record.tool_call_id} attempt {call.attempt}")
        interval = arguments.get("interval", 1)
        saved_task_id = _task_id_from_checkpoint(call.heartbeat_checkpoint)
        if saved_task_id is not None:
            # An earlier attempt started the task; never start a second one.
            return ToolTaskStarted(task_id=saved_task_id, poll_interval_seconds=interval)
        task_id = f"task-{uuid.uuid4().hex}"
        self._write_task(task_id, {"arguments": arguments, "polls": 0, "answer": None})
        self._invocations.record(f"create {record.tool_call_id}")
        assert call.save_heartbeat_checkpoint is not None
        await call.save_heartbeat_checkpoint(json.dumps({"report_task_id": task_id}))
        if "start_gate" in arguments:
            # Dex batches heartbeat checkpoints; repeating it lets it land before the test kills.
            for _ in range(8):
                await call.heartbeat()
                await asyncio.sleep(0.25)
            await _wait_at_gate(self._state_dir, str(arguments["start_gate"]))
        return ToolTaskStarted(task_id=task_id, poll_interval_seconds=interval)

    def _task_path(self, task_id: str) -> Path:
        return self._state_dir / "tasks" / f"{task_id}.json"

    def _read_task(self, task_id: str) -> dict[str, Any]:
        task: dict[str, Any] = json.loads(self._task_path(task_id).read_text())
        return task

    def _write_task(self, task_id: str, task: dict[str, Any]) -> None:
        path = self._task_path(task_id)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(task))


class TaskPollingRunner(TaskStartingRunner):
    """Starts tasks and polls them."""

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        record = poll.tool_call
        assert record.task_id is not None
        pending = poll.pending_answer
        self._invocations.record(
            f"poll {record.tool_call_id} attempt {poll.attempt} "
            f"pending {'none' if pending is None else pending.resolution}"
        )
        task = self._read_task(record.task_id)
        arguments = task["arguments"]
        if arguments.get("continuations"):
            self._invocations.record(
                f"continuation {record.tool_call_id} {record.input_continuation or 'none'}"
            )
        if "block_gate" in arguments and arguments.get("ignores_cancellation"):
            await self._wait_through_cancellation(record.tool_call_id, str(arguments["block_gate"]))
            self._invocations.record(f"poll {record.tool_call_id} returned")
            return ToolExecutionResult(output=f"report on {arguments['topic']}")
        if "block_gate" in arguments:
            await _wait_at_gate(self._state_dir, str(arguments["block_gate"]))
        if arguments.get("fail_polls"):
            raise RuntimeError("the task server is unreachable")
        if arguments.get("fail_task"):
            raise ToolExecutionError("the report failed")
        task["polls"] += 1
        if pending is not None and pending.resolution is QuestionResolution.ANSWERED:
            self._invocations.record(f"deliver {record.tool_call_id} {pending.answer}")
            task["answer"] = pending.answer
        elif pending is not None:
            if not arguments.get("takes_decline"):
                self._invocations.record(f"cancel {record.tool_call_id}")
                raise RuntimeError("the report task cannot take a declined question")
            self._invocations.record(f"deliver {record.tool_call_id} {pending.resolution}")
            task["declined"] = str(pending.resolution)
        self._write_task(record.task_id, task)
        if "declined" in task:
            return ToolExecutionResult(
                output=f"report on {arguments['topic']} without a format ({task['declined']})"
            )
        if arguments.get("ask") and task["answer"] is None:
            return ToolInputRequested(
                question=TASK_QUESTION,
                choices=["pdf", "html"],
                allows_free_text=False,
                continuation=json.dumps({"task_id": record.task_id}),
            )
        is_finished = task["polls"] >= int(arguments.get("polls", 2))
        if "poll_gate" in arguments:
            gate = FileGate(self._state_dir, str(arguments["poll_gate"]))
            gate.mark_reached()
            is_finished = gate.is_released()
        if not is_finished:
            continuation = (
                f"pending-{task['polls']}"
                if arguments.get("continuations") and task["polls"] % 2 == 1
                else None
            )
            return ToolTaskPending(
                poll_interval_seconds=arguments.get("interval", 1), continuation=continuation
            )
        suffix = "" if task["answer"] is None else f" as {task['answer']}"
        return ToolExecutionResult(output=f"report on {arguments['topic']}{suffix}")

    async def cancel_tool_task(self, cancellation: ToolTaskCancellation) -> None:
        record = cancellation.tool_call
        self._invocations.record(
            f"cancel-task {record.tool_call_id} attempt {cancellation.attempt} "
            f"{record.status} {record.cancellation_reason}"
        )
        assert record.task_id is not None
        task = self._read_task(record.task_id)
        if task["arguments"].get("fail_cancel"):
            raise RuntimeError("the task server refused the cancellation")
        task["cancelled"] = True
        self._write_task(record.task_id, task)

    async def _wait_through_cancellation(self, tool_call_id: str, gate_name: str) -> None:
        gate = FileGate(self._state_dir, gate_name)
        gate.mark_reached()
        while not gate.is_released():
            try:
                await asyncio.sleep(0.05)
            except asyncio.CancelledError:
                # A poll that ignores cancellation returns its result after the turn ended.
                self._invocations.record(f"poll {tool_call_id} cancelled")


def build_task_agent_flow(
    state_dir: Path,
    *,
    is_poller_missing: bool = False,
    interrupt_deadline: timedelta | None = None,
) -> AgentSessionFlow:
    runner = (TaskStartingRunner if is_poller_missing else TaskPollingRunner)(state_dir)
    return AgentSessionFlow(
        runner,
        flow_type=TASK_AGENT_FLOW_TYPE,
        options=AgentSessionOptions(
            tool_policies=TOOL_POLICIES, interrupt_deadline=interrupt_deadline
        ),
    )


def build_task_agent_flow_from_environment() -> AgentSessionFlow:
    deadline_seconds = os.environ.get(INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE)
    return build_task_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        is_poller_missing=os.environ.get(WITHOUT_POLLER_ENVIRONMENT_VARIABLE) == "1",
        interrupt_deadline=None
        if deadline_seconds is None
        else timedelta(seconds=float(deadline_seconds)),
    )


def _task_id_from_checkpoint(checkpoint: str | None) -> str | None:
    """Return the task ID this runner saved; a checkpoint another writer saved reads as none."""
    if checkpoint is None:
        return None
    try:
        saved = json.loads(checkpoint)
    except json.JSONDecodeError:
        return None
    task_id = saved.get("report_task_id") if isinstance(saved, dict) else None
    return task_id if isinstance(task_id, str) else None


async def _wait_at_gate(state_dir: Path, gate_name: str) -> None:
    gate = FileGate(state_dir, gate_name)
    gate.mark_reached()
    await gate.wait_for_release()


def _answer_with_results(turn: AgentTurnContext, calls: list[list[str]]) -> AgentTurnOutcome:
    messages: list[FrameworkMessage] = []
    summaries: list[str] = []
    for tool_call_id, tool_name, arguments in calls:
        record = turn.recorded_tool_call(tool_call_id)
        if record is None:
            raise RuntimeError(f"no record for {tool_call_id}")
        result = describe_tool_call_outcome(record)
        messages.append(
            FrameworkMessage(
                role=MessageRole.TOOL,
                tool_call_id=tool_call_id,
                tool_name=tool_name,
                tool_arguments=arguments,
                tool_output=result,
                framework_item=json.dumps({"tool_call_id": tool_call_id, "result": result}),
            )
        )
        summaries.append(f"{tool_name}: {result}")
    text = "; ".join(summaries)
    messages.append(
        FrameworkMessage(
            role=MessageRole.ASSISTANT, text=text, framework_item=json.dumps({"text": text})
        )
    )
    return TurnCompleted(new_messages=messages, usage=_MODEL_CALL_USAGE)
