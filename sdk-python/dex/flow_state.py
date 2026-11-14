# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass
from datetime import datetime, timezone
from enum import Enum

from dex.dexpb import dex_pb2 as pb


class ActiveStepExecutionPhase(Enum):
    """Tell where an open Step execution stands.

    .. versionadded:: 1.45.2

    Attributes:
        ACTIVE: The WaitFor or Execute method is pending, running, or waiting to
            retry. Read ``ActiveStepExecution.invocation`` for details.
        WAITING: WaitFor completed and the Step execution waits for its
            conditions. No Step method call is open.
    """

    ACTIVE = "active"
    WAITING = "waiting"


class StepMethodName(Enum):
    """Name the Step method that an open Step method call runs.

    .. versionadded:: 1.45.2

    Attributes:
        WAIT_FOR: The Step's ``wait_for`` method.
        EXECUTE: The Step's ``execute`` method.
    """

    WAIT_FOR = "wait_for"
    EXECUTE = "execute"


class StepMethodInvocationPhase(Enum):
    """Tell where the current attempt of an open Step method call stands.

    .. versionadded:: 1.45.2

    Attributes:
        PENDING: The attempt waits for its locks or for a Worker. Read
            ``StepMethodInvocation.pending_reason``.
        RUNNING: A Worker received the attempt.
        BACKOFF: The last attempt failed, and the next attempt waits for its
            retry backoff.
        UNSPECIFIED: The server reported no phase.
    """

    PENDING = "pending"
    RUNNING = "running"
    BACKOFF = "backoff"
    UNSPECIFIED = "unspecified"


class StepMethodPendingReason(Enum):
    """Tell why a pending Step method attempt has not reached a Worker yet.

    .. versionadded:: 1.45.2

    Attributes:
        WAITING_FOR_WORKER: The attempt waits for a Worker of its Flow type.
        WAITING_FOR_SUPPORTING_WORKER: Workers of the Flow type are connected,
            but each one declined the Step type.
        WAITING_FOR_LOCKS: Another Step or a transactional RPC holds one of the
            attempt's locks, or an earlier Step waits for one of them.
    """

    WAITING_FOR_WORKER = "waiting_for_worker"
    WAITING_FOR_SUPPORTING_WORKER = "waiting_for_supporting_worker"
    WAITING_FOR_LOCKS = "waiting_for_locks"


@dataclass(frozen=True)
class StepMethodInvocation:
    """Describe the open WaitFor or Execute method call of an open Step execution.

    .. versionadded:: 1.45.2

    Attributes:
        method: The Step method that this call runs.
        phase: Where the current attempt stands.
        attempt: The 1-based pending or running attempt. In the ``BACKOFF``
            phase it is the next attempt, so it is one more than
            ``StepMethodFailureInfo.attempt``.
        pending_reason: Why a ``PENDING`` attempt waits, or ``None`` in other
            phases.
        pending_since: When the current attempt became pending, as a
            timezone-aware UTC datetime, or ``None`` in the ``BACKOFF`` phase.
        attempt_started_at: When a Worker received the ``RUNNING`` attempt, as
            a timezone-aware UTC datetime, or ``None`` in other phases.
        backoff_fires_at: When the ``BACKOFF`` phase ends and the next attempt
            becomes pending, as a timezone-aware UTC datetime, or ``None`` in
            other phases.
    """

    method: StepMethodName
    phase: StepMethodInvocationPhase
    attempt: int
    pending_reason: StepMethodPendingReason | None
    pending_since: datetime | None
    attempt_started_at: datetime | None
    backoff_fires_at: datetime | None


@dataclass(frozen=True)
class StepMethodFailureInfo:
    """Describe the latest failed attempt of an open Step method call.

    Dex keeps only the latest failure. It does not report when that attempt
    failed. Read ``StepMethodInvocation.backoff_fires_at`` for when the next
    attempt starts.

    .. versionadded:: 1.45.2

    Attributes:
        attempt: The 1-based attempt that failed.
        error_type: The error type the Worker reported. A Python Worker reports
            the raised exception's module and qualified class name, such as
            ``billing.ChargeDeclinedError``. It is empty when Dex failed the attempt
            itself, for example when no Worker was available.
        detail: The failure message. It is the Worker's error message, or the
            server's description when the Worker reported none.
        stack_trace: The Worker-language stack trace, or ``""`` when the Worker
            sent none.
        backend_error: The Dex failure category, such as
            ``FLOW_ERROR_TYPE_WORKER_API_FAIL``. Use it only for logs. Its
            spellings can differ between server versions.
    """

    attempt: int
    error_type: str
    detail: str
    stack_trace: str
    backend_error: str


@dataclass(frozen=True)
class ActiveStepExecution:
    """Describe one open Step execution in a ``FlowState``.

    .. versionadded:: 1.45.2

    Attributes:
        step_execution_id: The Step execution ID, such as ``ChargeStep-1``. It
            equals ``Context.step_execution_id`` inside the Step's handlers.
        step_type: The registered Step type.
        execution_number: The positive execution number of the Step type in this
            run. Pass ``StepExecutionId(step_type, execution_number)`` to
            ``Client.wait_for_step_completion`` or ``Client.skip_timer`` to
            address this Step execution.
        phase: Whether a Step method call is open or the Step waits for its
            conditions.
        invocation: The open WaitFor or Execute method call, or ``None`` when
            no call is open. No call is open in the ``WAITING`` phase.
        last_failure: The latest failed attempt of the open Step method call,
            or ``None`` when none failed. A value here while
            ``StepMethodInvocation.phase`` is ``BACKOFF``, or while a later
            attempt is pending or running, means that the Step is retrying
            after a failure.
    """

    step_execution_id: str
    step_type: str
    execution_number: int
    phase: ActiveStepExecutionPhase
    invocation: StepMethodInvocation | None
    last_failure: StepMethodFailureInfo | None


@dataclass(frozen=True)
class FlowState:
    """Describe the live Step state and task queue of the current or latest run.

    ``Client.get_flow_state`` and ``AsyncClient.get_flow_state`` return it. Read
    it to see which Step executions are open, whether one of them is retrying
    after a failed attempt, and which task queue runs the Flow. See
    ``Client.get_flow_state`` for an example.

    .. versionadded:: 1.45.2

    Attributes:
        active_step_executions: The open Step executions of the run, in server
            order. It is empty when the run is closed or has no open Step
            execution.
        task_queue: The task queue whose Workers run this Flow's Steps and
            RPCs. ``""`` means the default task queue. Dex fixes the task queue
            when the Flow starts, so it does not change during a run. Read it
            from a Client without a Worker to see which Workers must serve the
            Flow. See ``StartFlowOptions.task_queue``.
    """

    active_step_executions: Sequence[ActiveStepExecution]
    task_queue: str


def flow_state_from_proto(flow_id: str, response: pb.GetFlowStateResponse) -> FlowState:
    """Map a GetFlowState response into the public FlowState."""
    return FlowState(
        active_step_executions=tuple(
            _map_active_step_execution(flow_id, step)
            for step in response.active_step_executions
        ),
        task_queue=response.flow_config.worker_target.task_queue,
    )


def _map_active_step_execution(
    flow_id: str, step: pb.ActiveStepExecutionState
) -> ActiveStepExecution:
    step_type_part, separator, number_part = step.step_execution_id.rpartition("-")
    execution_number = int(number_part) if number_part.isdigit() else 0
    if separator == "" or step_type_part == "" or execution_number < 1:
        raise RuntimeError(
            f"Dex returned a malformed Step execution ID {step.step_execution_id!r} "
            f"for Flow {flow_id}"
        )
    invocation = None
    if step.HasField("wait_for_invocation"):
        invocation = _map_invocation(StepMethodName.WAIT_FOR, step.wait_for_invocation)
    elif step.HasField("execute_invocation"):
        invocation = _map_invocation(StepMethodName.EXECUTE, step.execute_invocation)
    return ActiveStepExecution(
        step_execution_id=step.step_execution_id,
        step_type=step.step_type,
        execution_number=execution_number,
        phase=(
            ActiveStepExecutionPhase.WAITING
            if step.phase == pb.ACTIVE_STEP_PHASE_WAITING
            else ActiveStepExecutionPhase.ACTIVE
        ),
        invocation=invocation,
        last_failure=(
            _map_failure(step.last_failure_info)
            if step.HasField("last_failure_info")
            else None
        ),
    )


def _map_invocation(
    method: StepMethodName, invocation: pb.StepMethodInvocationState
) -> StepMethodInvocation:
    return StepMethodInvocation(
        method=method,
        phase=_INVOCATION_PHASES.get(
            invocation.phase, StepMethodInvocationPhase.UNSPECIFIED
        ),
        attempt=invocation.attempt,
        pending_reason=_PENDING_REASONS.get(invocation.pending_reason),
        pending_since=(
            invocation.pending_since_time.ToDatetime(tzinfo=timezone.utc)
            if invocation.HasField("pending_since_time")
            else None
        ),
        attempt_started_at=(
            invocation.attempt_started_time.ToDatetime(tzinfo=timezone.utc)
            if invocation.HasField("attempt_started_time")
            else None
        ),
        backoff_fires_at=(
            invocation.backoff_fires_at_time.ToDatetime(tzinfo=timezone.utc)
            if invocation.HasField("backoff_fires_at_time")
            else None
        ),
    )


def _map_failure(failure: pb.StepMethodFailure) -> StepMethodFailureInfo:
    details = failure.details
    return StepMethodFailureInfo(
        attempt=failure.attempt,
        error_type=details.original_worker_error_type,
        detail=details.original_worker_error_detail or details.detail,
        stack_trace=details.original_worker_error_stack_trace,
        backend_error=failure.backend_error,
    )


_INVOCATION_PHASES = {
    pb.STEP_METHOD_INVOCATION_PHASE_PENDING: StepMethodInvocationPhase.PENDING,
    pb.STEP_METHOD_INVOCATION_PHASE_RUNNING: StepMethodInvocationPhase.RUNNING,
    pb.STEP_METHOD_INVOCATION_PHASE_BACKOFF: StepMethodInvocationPhase.BACKOFF,
}

_PENDING_REASONS = {
    pb.STEP_METHOD_PENDING_REASON_WAITING_FOR_WORKER: (
        StepMethodPendingReason.WAITING_FOR_WORKER
    ),
    pb.STEP_METHOD_PENDING_REASON_WAITING_FOR_SUPPORTING_WORKER: (
        StepMethodPendingReason.WAITING_FOR_SUPPORTING_WORKER
    ),
    pb.STEP_METHOD_PENDING_REASON_WAITING_FOR_LOCKS: (
        StepMethodPendingReason.WAITING_FOR_LOCKS
    ),
}
