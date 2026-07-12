# Server-Side Condition Evaluation Design

## Overview

This document describes when and how the dex server evaluates WaitFor
conditions on waiting steps. The core principle is **reserve-on-promote,
splice-on-execute-complete**: during server-driven promotion the server sets
step status to `INVOKING_EXECUTE`, persists `ConditionResults` (the
reservation), and allocates `executeMethodExeID` — but it does **not** remove
messages from `UnconsumedChannelMessages`. The splice happens later, in
`ProcessStepExecuteCompleted`, after the worker confirms Execute finished.

## Responsibility Split

### Worker (run is `Running`)

When a worker holds a run (status = `Running`), it is solely responsible for
evaluating WaitFor conditions. This applies to:

- **Internal channel publishes**: When a step's Execute or WaitFor publishes
  messages to internal channels, the worker locally evaluates whether those
  messages satisfy any other step's WaitFor condition. If satisfied, the worker
  transitions the step and reports the full set of transitions to the server in
  a single request.

- **In-memory timers**: While the worker holds the run, timer conditions are
  managed in-memory by the worker. No durable timers are needed.

- **Timer-triggered dispatch**: When a durable timer fires and the server
  dispatches the run, the `durable_timer_fire_at` timestamp is forwarded to
  the worker via `DispatchRunRequest`. The worker uses
  `durable_timer_fire_at` directly as `effectiveNow` when evaluating timer
  conditions, consistent with the server-side evaluation.

The server simply persists whatever the worker reports — it does not
second-guess or reevaluate.

### Server (run is `AllStepsWaitingForConditions`)

When all steps are waiting and no worker holds the run, the server promotes
satisfied steps in response to external events using a
**reserve-on-promote** pattern:

1. **External channel messages** (`ProcessExternalChannelMessagesReceived`):
   When an external publish arrives, the server stores the new messages and
   evaluates all waiting steps via `serverWakePromoteIfAny`. For each
   satisfied step, it sets the step status to `INVOKING_EXECUTE`, persists
   `ConditionResults`, and allocates `executeMethodExeID`. It then sets the
   run status to `Pending` and creates a `run_resume_dispatch_task`. The
   unconsumed message queue is **not** shortened yet — that happens in
   `ProcessStepExecuteCompleted`.

2. **Durable timer fired** (`ProcessStepWaitForTimerFired`): When a durable
   timer fires, the server evaluates all waiting steps with `effectiveNow =
   fire_at` via `serverWakePromoteIfAny`. Satisfied steps are promoted to
   `INVOKING_EXECUTE` with persisted `ConditionResults` and
   `executeMethodExeID`. The run is set to `Pending` with a dispatch task
   carrying `DurableTimerFireAt`. The queue is **not** spliced here. For AllOf
   with unsatisfied channel conditions, the server reschedules the timer for
   remaining steps.

**Validation constraint**: Each `WaitForCondition` (AnyOf or AllOf) has at
most one timer condition. This is enforced by `Validate()`.

## Durable Timer Design

### Creation

When the server computes that a run's new status is
`AllStepsWaitingForConditions`, it scans all waiting steps for timer conditions
and creates a **single durable timer** for the earliest `fire_at_unix_ms`
across all steps — unless an existing timer already covers it (lazy reuse).

The timer task records which step triggered creation (debug-only):

```
TimerTaskInfo {
    RunID              string
    Namespace          string
    CreatedByStepExeID string   // debug-only: which step triggered creation
}
```

The `RunRow` tracks the active timer via:

- `ActiveDurableTimerID` — the timer task ID
- `DurableTimerFireAt` — the timer's fire time (also the task's SortKey)

### Lazy Timer Reuse

`createDurableTimerIfNeeded` checks whether an existing durable timer
(`ActiveDurableTimerID` / `DurableTimerFireAt`) fires at or before the
earliest needed time. If so, it returns nil (reuse) instead of creating a new
timer. This maximizes the value of already-created timers.

Timer fields are **never eagerly cleared**. Even when no current step needs
the timer, future steps created by the worker may reuse it. A stale timer
that fires for a run no longer in `AllStepsWaitingForConditions` is simply a
no-op.

### Firing

When the timer fires, `ProcessStepWaitForTimerFired`:

1. Guards: run must be `AllStepsWaitingForConditions` and
   `ActiveDurableTimerID` must match the timer task ID.
2. Uses `effectiveNow = fire_at` directly.
3. Iterates all waiting steps with a timer condition at or before
   `effectiveNow`. For each, calls `EvaluateCondition` to confirm the
   **overall** condition is satisfied (handles AllOf with channel requirements).
4. If any step is satisfied: promotes via `serverWakePromoteIfAny` (sets step
   status to `INVOKING_EXECUTE`, persists `ConditionResults`,
   `executeMethodExeID`), sets `Pending`, creates
   `ImmediateTaskRunResumeDispatch` with `DurableTimerFireAt = effectiveNow`.
   The queue is **not** spliced here.
5. If no step is satisfied: reschedules via `createDurableTimerIfNeeded`.

The dispatch task's `DurableTimerFireAt` is forwarded through
`handleDispatchTask` → `DispatchRunRequest` → matching → worker, so the
worker can evaluate with the correct `effectiveNow`.

### Edge Cases

**Stale/superseded timer**: If the run is no longer in
`AllStepsWaitingForConditions` or the `ActiveDurableTimerID` doesn't match,
the timer is silently ignored (no-op).

**AllOf with unsatisfied channels**: Timer fires, timer sub-condition is met,
but channel sub-conditions are not → overall condition not satisfied →
reschedule. When the channel eventually arrives,
`ProcessExternalChannelMessagesReceived` evaluates and dispatches.

## Why This Design

1. **Reserve-on-promote, splice-on-execute-complete**: The server promotes
   waiting steps (sets `INVOKING_EXECUTE` + persists `ConditionResults` +
   allocates `executeMethodExeID`) but defers the queue splice to
   `ProcessStepExecuteCompleted`. This keeps the unconsumed queue as the
   ground truth for available messages, while reservations prevent
   over-promotion when multiple steps race for the same channel.

2. **Clear ownership**: Worker owns running state; server owns idle state.
   No ambiguity about who evaluates conditions at any point.

3. **Reservation-aware evaluator**: `server/internal/engine/evaluate/` is the
   canonical server-side evaluator. It is a symmetric port of
   `sdk-go/dex/evaluate/` using persistence types instead of proto types. It
   accounts for reservations held by concurrent `INVOKING_EXECUTE` siblings
   before promoting new steps, preventing over-promotion on the same channel.
   **AnyOf uses greedy evaluation**: all branches that are currently satisfied
   are marked and consumed, not just the first one. A step waking from
   `AnyOf(timer, channelA, channelB)` may receive timer-fired=true _and_
   consumed messages from both channels if all three conditions were met at
   promotion time.

4. **Lazy timer reuse**: Durable timers are never eagerly cleared, maximizing
   reuse across run lifecycle transitions. `createDurableTimerIfNeeded`
   encapsulates the reuse check internally.

5. **Consistent evaluation**: `DurableTimerFireAt` is forwarded through the
   dispatch chain to the worker, so server and worker evaluate with the exact
   same timestamp.