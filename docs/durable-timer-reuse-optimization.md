# Durable Timer Reuse Optimization

## Motivation

Previously, the server created a **new** durable timer every time a run
entered `AllStepsWaitingForConditions` and eagerly cleared timer state on
every dispatch. This was wasteful: a run frequently cycles through
`AllStepsWaiting → Pending → Running → AllStepsWaiting`, and the earliest
needed timer fire time often stays the same or gets later across cycles.

This optimization introduces **lazy timer reuse**: we never eagerly clear
timer fields, and `createDurableTimerIfNeeded` skips creating a new timer when
an existing one already fires at or before the earliest needed time.

Additionally, both server-side condition handlers (`ProcessStepWaitForTimerFired`
and `ProcessExternalChannelMessagesReceived`) now follow a unified
**evaluate-only** pattern: the server confirms a step's condition is satisfied
but does not modify step execution status or consume channels. The worker
handles all actual state transitions when it picks up the run.

### Worker-side timers complement the server-side wake-up

The server's durable timer does NOT "fire" a condition; it only **wakes the
run up** when the run is in `AllStepsWaitingForConditions` and no worker
holds the stream. The wake-up flow is:

1. Server's durable timer fires at `FireAtUnixMs`.
2. `ProcessStepWaitForTimerFired` re-evaluates with
   `effectiveNow = req.FireAtUnixMs` purely to decide whether to dispatch.
3. If the condition would be satisfied, the run transitions
   `AllStepsWaitingForConditions → Pending` and a dispatch task is enqueued.
4. The worker that picks it up gets `pr.DurableTimerFired = true` and
   `pr.ServerTimestampMs`, then runs the **same** local condition evaluator
   (force-firing any TimerCondition because of `DurableTimerFired`) to
   actually consume channels / promote the step / invoke Execute.

While the worker holds a `Running` run, the same re-evaluation needs to
happen promptly when a sibling step's timer is due. There's no need for
the server's wake-up path here (the worker is already attached), so the
SDK arms its own `time.AfterFunc` per `TimerCondition` and synchronously
checkpoints any resulting unblock via `RunsService.ProcessStepsUnblocked`
before invoking Execute. The server-side fire that races into this window
is dropped (counted by
`step_wait_for_timer_fired_dropped_not_all_waiting_counter`) because the
run isn't `AllStepsWaitingForConditions` — there's nothing to wake up.

See [wait-for-conditions-design.md](wait-for-conditions-design.md) for
the full protocol and crash-window correctness argument.

## Data Model

### RunRow

Two fields track the active durable timer:

```
ActiveDurableTimerID  string  // bson:"active_durable_timer_id"
DurableTimerFireAt    int64   // bson:"durable_timer_fire_at"
```

`ActiveDurableTimerID` is the timer task's unique ID.
`DurableTimerFireAt` is the timer's fire time (= task SortKey).

These fields are **never eagerly cleared**. They are only updated when
`createDurableTimerIfNeeded` creates a new timer (which replaces the old one).
A stale timer that fires for a run no longer in `AllStepsWaitingForConditions`
is simply a no-op.

### TimerTaskInfo

```
CreatedByStepExeID  string  // bson:"created_by_step_exe_id,omitempty"
```

Debug-only. Records which step triggered timer creation, but the timer may
fire and be used by a different step due to lazy reuse.

### ImmediateTaskInfo

```
DurableTimerFireAt  int64  // bson:"durable_timer_fire_at,omitempty"
```

Non-zero when a dispatch was triggered by a durable timer firing. Forwarded
through `handleDispatchTask` → `DispatchRunRequest` → matching → worker.

### DispatchRunRequest (proto)

```protobuf
int64 durable_timer_fire_at = 6;
```

The worker uses `durable_timer_fire_at` directly as `effectiveNow` when
evaluating timer conditions, consistent with the server-side evaluation.

### Validation

Each `WaitForCondition` (AnyOf or AllOf) has **at most one** `TimerCondition`.
This is enforced by `WaitForCondition.Validate()`. The constraint simplifies
timer management: each waiting step has at most one timer sub-condition, so
the "earliest timer across all steps" is well-defined and unambiguous.

## Lazy Timer Reuse

### Core Idea

Once a durable timer is created and its task row inserted into the timer task
queue, it will fire at the scheduled time regardless of run state. Rather than
eagerly clearing it and creating new timers, we keep it and check whether an
existing timer is still useful before creating a new one.

### createDurableTimerIfNeeded

This is the central function. It scans all active waiting steps for timer
conditions, finds the minimum `fire_at_unix_ms` (call it `minFireAt`), and
then:

1. If `minFireAt == 0` (no timer conditions exist): return `(nil, 0)`.
2. If `run.ActiveDurableTimerID != ""` and `run.DurableTimerFireAt <= minFireAt`:
   return `(nil, minFireAt)` — **reuse the existing timer**.
3. Otherwise: create a new `TimerTaskRow` with `SortKey = minFireAt`.

The reuse check at step 2 is the key optimization. It fires when:

- **Same step, same timer**: The run cycled through Running and back to
  AllStepsWaiting with the same timer condition.
- **Different step, earlier or equal timer**: The original step was unblocked,
  but a new step has a timer at or after the existing timer's fire time.
- **Future steps**: The worker created new steps with timer conditions, but the
  existing timer fires before (or at) the earliest new condition.

### When the Existing Timer Fires Too Early

If the existing timer fires at `T1` and the earliest needed time is `T2 > T1`,
the timer fires at `T1`. `ProcessStepWaitForTimerFired` evaluates all waiting
steps with `effectiveNow = max(now, T1)`. Since no step has
`FireAtUnixMs <= T1` (all are at `T2` or later), no step is satisfied.
The handler calls `createDurableTimerIfNeeded`, which creates a new timer
at `T2`. One extra round trip, but correct.

### When the Existing Timer Is Never Needed

If the run completes or moves to a terminal state, the timer fires and sees
`run.Status != AllStepsWaitingForConditions` → returns nil (no-op). The timer
task row is dequeued and discarded. The stale `ActiveDurableTimerID` /
`DurableTimerFireAt` on the run row are harmless.

## Evaluate-Only Pattern

Both `ProcessStepWaitForTimerFired` and `ProcessExternalChannelMessagesReceived`
follow the same pattern when `run.Status == AllStepsWaitingForConditions`:

```
1. Load run
2. Iterate all waiting steps
3. For each, call EvaluateCondition(...)
4. If any step's overall condition is satisfied:
   a. Set run status to Pending
   b. Create ImmediateTaskRunResumeDispatch
   c. Do NOT modify activeStepExecutions
   d. Do NOT consume channels (no ReplaceUnconsumedChannels)
   e. Do NOT clear timer fields
   f. Break after first satisfied step
5. If no step satisfied:
   - Timer handler: reschedule via createDurableTimerIfNeeded
   - Channel handler: just store the new messages (no further action)
6. CAS update
```

### Why Evaluate-Only?

The server confirms feasibility but defers actual transitions to the worker:

- **Simpler CAS updates**: The CAS only touches run status and task creation.
  No step execution mutations, no channel replacement. This reduces the
  surface area for conflicts.
- **Worker atomicity**: The worker sees the full run state (including any
  channel messages that arrived between server evaluation and worker pickup)
  and performs step transitions + channel consumption atomically.
- **Unified pattern**: Both timer and channel handlers use the same logic,
  reducing code duplication and making the behavior predictable.

### AllOf Handling

For `AllOf(timer, channel)` where the timer fires but the channel hasn't
arrived:

1. Timer fires → `ProcessStepWaitForTimerFired` evaluates the step.
2. `EvaluateCondition` returns `Satisfied = false` (timer sub-condition met,
   channel sub-condition not met).
3. No dispatch. Handler reschedules for other steps if needed.
4. Later, external channel message arrives →
   `ProcessExternalChannelMessagesReceived` evaluates.
5. Now `effectiveNow >= FireAtUnixMs` naturally (time has moved past the timer),
   so the timer sub-condition is satisfied. Channel sub-condition also
   satisfied. `EvaluateCondition` returns `Satisfied = true`.
6. Server sets Pending, creates dispatch. Worker handles the rest.

## Timer Lifecycle

```
            ┌─────────────────────────────────────────────────────────────┐
            │              Run lifecycle with lazy timer reuse            │
            └─────────────────────────────────────────────────────────────┘

  Worker completes step → computeNewStatus = AllStepsWaiting
       │
       ▼
  createDurableTimerIfNeeded(...)
       │
       ├── existing timer covers it (DurableTimerFireAt <= minFireAt)
       │       → REUSE: no new timer created, no field updates
       │
       └── no existing timer or fires too late
               → CREATE: new TimerTaskRow, update ActiveDurableTimerID
                         + DurableTimerFireAt

  ─── timer fires ───

  ProcessStepWaitForTimerFired:
       │
       ├── run not AllStepsWaiting → no-op (stale)
       ├── ActiveDurableTimerID mismatch → no-op (superseded)
       │
       └── evaluate all waiting steps with effectiveNow
              │
              ├── step satisfied → set Pending, dispatch w/ DurableTimerFireAt
              │                    (no step move, no channel consumption)
              │
              └── no step satisfied → reschedule via createDurableTimerIfNeeded
                                      (may reuse if existing timer still valid)

  ─── external channel message ───

  ProcessExternalChannelMessagesReceived (AllStepsWaiting case):
       │
       ├── store messages in UnconsumedChannelMessages
       ├── evaluate all waiting steps with merged messages
       │
       ├── step satisfied → set Pending, dispatch
       │                    (no step move, no channel consumption, no timer clear)
       │
       └── no step satisfied → just store messages (CAS with messages only)
```

## DurableTimerFireAt Forwarding

When a timer-triggered dispatch is created, `DurableTimerFireAt = effectiveNow`
is set on the `ImmediateTaskInfo`. The forwarding chain:

1. `ProcessStepWaitForTimerFired` → `ImmediateTaskInfo.DurableTimerFireAt`
2. `handleDispatchTask` → `DispatchRunRequest.DurableTimerFireAt`
3. Matching layer → worker

The worker uses `DurableTimerFireAt` directly as `effectiveNow` when evaluating
timer conditions, consistent with the server-side evaluation.

For non-timer dispatches (channel-triggered or initial), `DurableTimerFireAt`
is 0, and the worker uses `now` as usual.

## Comparison with Previous Design

| Aspect | Previous | Current |
|---|---|---|
| Timer creation | Every AllStepsWaiting transition | Only when no valid timer exists |
| Timer clearing | Eager on every dispatch | Never — stale timers are no-ops |
| Step modification | Server moves step to InvokingExecute | Worker handles step transitions |
| Channel consumption | Server consumes via ReplaceUnconsumedChannels | Worker consumes channels |
| CAS complexity | Status + step + channels + timer | Status + dispatch task only |
| Timer per AllOf | Could fire and move step even with unsatisfied channels | Full evaluation prevents false dispatches |
| Timer conditions | Unlimited per WaitForCondition | At most one per WaitForCondition |
