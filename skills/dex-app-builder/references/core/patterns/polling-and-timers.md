# Polling and timer patterns

Part of the [design patterns](../patterns.md), which hold the pseudocode conventions and the runnable examples.

## Polling

Use Polling whenever a Flow waits for an external system to become ready or finish: a cloud build, a rollout, a provisioning job, a third-party status. Business signals inside Dex (Channels, RPCs) and business timers (TTL, reminders, inactivity) still use WaitFor; they are not polling. Polling has two shapes. Choose one per wait:

| Question | [WaitFor Timer](#polling-with-waitfor-timer) | [Long-running Step heartbeat](#polling-with-long-running-step-heartbeat) |
| --- | --- | --- |
| What is one round? | A new Step execution, recorded in the Flow's history | A heartbeat or Stream frame inside one attempt |
| Cost | Somewhat heavier: records every round in history | Lighter: one Step execution for the whole wait |
| Between rounds | A durable Timer wait; no Worker task slot is held | The attempt holds a Worker task slot |
| Choose it for | Long intervals, seeing every round, Time Travel back to a round | Short intervals, the lightest loop |

The Timer shape records every round in the Flow's history, so each round is visible and Time Travel can move the run back to a round inside its recent history window. The heartbeat shape is lighter. What a Dex Server failover does to a waiting or running round is in the [high availability reference](https://docs.superdurable.io/reference/server/high-availability#lease).

### Polling with WaitFor Timer

Each round waits for a Timer in WaitFor, calls the provider once in Execute, and moves to itself while the work is not finished. Go folder: `polling-with-timer`.

```text
StartJob.execute(request)
    jobId = provider.startJob(idempotencyKey = flowId, cancel = context's cancellation signal)
    return goTo(CheckJobStatus, {jobId, round = 1})

CheckJobStatus options:
    execute retry            = a few attempts with backoff
    execute failure route    = RecordJobWaitFailure

CheckJobStatus.waitFor(context, input)
    return until(Timer(interval))

CheckJobStatus.execute(context, input)
    status = provider.getStatus(input.jobId, cancel = context's cancellation signal)   // error -> return error; retry policy re-runs
    if status is succeeded: return gracefulComplete(result)
    if status is failed:    return forceFail(reason)
    if input.round >= maxRounds: return forceFail("did not finish")
    return goTo(CheckJobStatus, {input.jobId, input.round + 1})
```

- Prefer "not ready yet" as a normal decision: GoTo the same Step. Returning it as an error mixes not-ready with real failures and spends the retry budget.
- Bound the loop with a round limit, the Flow timeout, or a watchdog Timer. A round number in the input bounds the number of checks, not elapsed time: calls, retries, and queueing add time to each round. A durable deadline that polling retries cannot delay uses the Flow timeout or a deadline watchdog.
- The polling interval Timer has already fired when Execute begins, so it cannot enforce a deadline while Execute retries. Computing the next round's Timer from a stored deadline does not cover that retrying round. Use a deadline watchdog when the deadline must trigger work during a retry.
- Keep the input small and fixed in size, such as the job ID and round number. Keep accumulated state in Attributes, never in a growing input.
- WaitFor only declares the Timer. The provider call runs in Execute.
- The retry policy covers transient errors and Worker crashes. Route exhausted retries with the execute failure route.
- For an interval of a few seconds or less, use the heartbeat shape.
- Pass the cancellation signal to provider calls that accept it. Prefer giving a blocking call that cannot observe cancellation its own timeout: a stuck call keeps its Worker slot until it returns, while the method timeout still ends the attempt and Dex schedules later attempts by the retry policy.

Never:

- Call the provider in WaitFor.
- Loop without any bound.
- Grow the Step input each round.
- Start a new Flow for each round or page.

#### Deadline watchdog

Use it for a deadline shorter than the Flow that must fire while work, such as a polling loop or a retrying Step, is still running. The work runs as the only branch of a Join, and the join Step races the branch's result against a Timer.

```text
JobResult = Join<JobOutcome>("job-result")        // register it with the Flow's Channels

StartJob.execute(request)
    jobId = provider.startJob(...)
    return goToMany(JobResult.fork(
        [round -> CheckJobStatus({jobId, round})],
        round -> AwaitJob(round)))

CheckJobStatus.waitFor(context, input) -> until(Timer(interval))
CheckJobStatus.execute(context, input)            // failure route -> ReportJobFailure
    status = provider.getStatus(input.jobId, cancel = context's cancellation signal)
    if status is finished: return JobResult.endBranch(input.round, status)
    return goTo(CheckJobStatus, input)            // the round token travels with each poll

ReportJobFailure.execute(input) -> return JobResult.endBranch(input.round, failed)

AwaitJob.waitFor(round) -> anyOf(JobResult.forFirstBranch(round), Timer(deadline))
AwaitJob.execute(context, round)
    if context.hasTimerFired():
        record overdue
        return gracefulComplete(overdue).cancelSteps(CheckJobStatus, ReportJobFailure)
    result = JobResult.branchResults(round)[0]
    record result
    return gracefulComplete(result)
```

- The join Step is the only decider. It needs no lock. Branches report with endBranch.
- Execute checks which Condition fired: the Timer means overdue; otherwise it reads the branch result.
- The poller's next rounds come from goTo, not from the fork, so they are not siblings of the join Step. Cancel the branch's Step types with cancelSteps. A cancel by Step type is correct while one round is active.
- A result that the branch reports after the Timer won stays pending in its round. It is harmless and does not block gracefulComplete.

### Polling with long-running Step heartbeat

One long-running Execute owns the whole wait. Go folder: `polling`.

```text
StartJob.execute(request)
    jobId = provider.startJob(idempotencyKey = flowId, cancel = context's cancellation signal)
    return goTo(AwaitJob, jobId)

AwaitJob options:
    execute method timeout   = maximum wait
    execute retry            = a few attempts (for example 3-5) with backoff,
                               total duration = maximum wait
    heartbeat timeout        = the default, or the longest silent interval plus a margin
    execute failure route    = RecordJobWaitFailure

AwaitJob.execute(context, jobId)
    lastReported = context.lastHeartbeatValue()
    loop:
        status = provider.getStatus(jobId, cancel = context's cancellation signal)   // error -> return error; retry policy re-runs
        if status is succeeded: return gracefulComplete(result)
        if status is failed:    return forceFail(reason)      // a definite business failure
        if status != lastReported: JobProgress.write(status); lastReported = status
        else: context.recordHeartbeat(lastReported)
        sleep(interval)

RecordJobWaitFailure.execute(context)
    read context.recoveryError() (detail, error type); record the business failure
```

- Prefer "not ready yet" as a normal loop branch; as an error it mixes with real failures and spends the retry budget.
- Keep the longest gap between heartbeats, including the slowest healthy call and the interval, below the heartbeat timeout the Step uses, with a margin. Its default is in each SDK's [StepOptions reference](https://docs.superdurable.io/reference/go/step-options#defaults).
- Keep the default heartbeat timeout when the loop fits it, so a stalled Worker is detected quickly. Raise it only when a healthy call is silent longer.
- Within that bound, use a few seconds when a user watches something they just started, and tens of seconds for long jobs.
- A business deadline that is shorter than the whole wait, or that the loop's Step options cannot hold because the Step re-enters itself to commit each status change, uses the [deadline watchdog](#deadline-watchdog): the waiting Step is the Join's branch, and the join Step races its result against a Timer.
- The retry policy covers Worker crashes and transient errors. Use a few attempts, for example 3 to 5, with backoff, and set the total duration to the maximum wait. Each attempt lasts at most the method timeout. Never use a large attempt count as a time bound.
- Use the language's plain sleep. Honor cancellation through the language SDK's mechanism. When the provider call accepts a cancellation argument, pass it. Prefer giving a blocking call that cannot observe cancellation its own timeout: a stuck call keeps its Worker slot until it returns, while the heartbeat or method timeout still ends the attempt and Dex schedules later attempts by the retry policy. Cancellation is cooperative: the loop exits at the next boundary that can report it. See [Make the Step stop](https://docs.superdurable.io/reference/server/runtime-data#cancel-honour).
- The wait limits come from the Step options, and Dex enforces each one: the method timeout ends an attempt, and the total duration is the retry budget. The Step code never computes a deadline, and neither the checkpoint nor the input carries one. A hard business deadline uses the [deadline watchdog](#deadline-watchdog).
- The checkpoint is only resume state, such as the last reported status. Resuming from the last stored checkpoint reduces repeated progress frames; it can be lost, so consumers tolerate duplicates and gaps. See [Heartbeat](https://docs.superdurable.io/reference/go/step-options#heartbeat).
- A business condition that is not a fixed duration, such as a lease the Flow does not own expiring, is still checked inside the loop.
- Write Stream frames only on status changes. Consumers recover from Attributes or a read-only RPC.
- Do not hold an Execute lock for the whole wait. Every Step that needs the lock would wait until the loop ends, and every RPC that needs it would fail. Commit locked results in a short following Step.
- The recovery error does not carry the last status. The Stream and logs do. To branch on which limit ended the wait, follow [recovery error types](../error-handling.md#recovery-error-types).
- Either durability works. With ASYNC, work whose result was not yet saved can run again; the checkpoint is only a resume hint.

Never:

- Run a long loop that never heartbeats or writes progress.
- Hand-write a deadline that duplicates the method timeout or the retry total duration.
- Pass a page token to a new execution to iterate.

## Durable timers

A Timer is a durable Condition, not an in-process timer. A skipped Timer acts as if it fired.

### Cron schedule

Fixed-interval recurring work in one durable Flow. It is not calendar cron. Go folder: `cron`.

```text
ValidateSchedule.execute(schedule) -> validate positive interval and run count, else forceFail
                          goTo(WaitForSchedule, state)
WaitForSchedule.waitFor -> AnyOf(Timer(interval), TriggerNow.ForOne(), SkipNext.ForOne())
WaitForSchedule.execute
    if SkipNext consumed: next occurrence without running (gracefulComplete if it was the last)
    else if last occurrence: goTo(Run)
    else: goToMany(Run(occurrence), WaitForSchedule(next))
Run.execute -> do the work idempotently; deadEnd() (gracefulComplete on the last run)
rpc triggerNow -> TriggerNow.publish()
```

- Start the next WaitForSchedule together with Run, so a slow run does not drift the schedule.
- Make each run's external effect idempotent per occurrence.

### Reminder

Send reminders until the recipient opts out. Go folder: `reminders`.

```text
Remind.waitFor(occurrence) -> AnyOf(Timer(interval), OptOut.ForOne())
Remind.execute(occurrence) -> if the Timer did not fire: gracefulComplete()
                              if OptOut.size() > 0: gracefulComplete()   // the Timer won, but an opt-out is queued
                              send the reminder, keyed by Flow ID + occurrence; goTo(Remind, occurrence + 1)
rpc optOutReminders -> OptOut.publish()
```

- Only one Step consumes the opt-out Channel. The Execute checks `OptOut.size()` also when the Timer won. An opt-out that arrives while this reminder is being sent may not stop it; the next wait includes the opt-out, so it stops the following one.
- When the business needs the opt-out to stop the reminder up to the decision commit, the opt-out RPC and the deciding Step share a lock, and an unlocked Step sends after the commit.
- The grace counts per decided action or branch. When the business allows only one such action overall after the opt-out, decide actions one at a time.
- One message reaches one consumer, so parallel waiters each get their own stop message, such as one per branch on a ChannelMap. A stop Attribute records the fact but wakes no waiter, because WaitFor has no Attribute Condition.
- Key each send by a stable occurrence ID that the receiver recognizes and deduplicates, so a retried Execute does not deliver twice.
- The runnable reminders examples show the single-consumer relative-Timer loop. Their send call has no occurrence idempotency key.

### Idle Timer

Act only after a period with no activity. Go folder: `inactiveness-tracker-timer`.

```text
Track.waitFor -> AnyOf(Timer(window), Activity.ForOne())
Track.execute -> if the Timer fired: goTo(ProcessInactivity)
                 else: goTo(Track)          // a fresh Timer after each activity
rpc recordActivity -> Activity.publish()
```

After each activity, the next WaitFor starts a full Timer(window). Computing the rest of the window from a stored timestamp also works; a fresh Timer is less code and skippable in tests.

Keep the deadline in the Flow where the activity happens. A separate Flow that needs a message on every operation is the wrong boundary.

### Idle Timer with Resolution

Take inbound messages, such as replies in a conversation or ticket, until a resolution arrives or the conversation goes quiet. This extends the Idle Timer. Go folder: `inbound-event-loop`. Runnable examples: [Go](https://github.com/superdurable/dex/tree/main/examples/go/patterns/inbound-event-loop), [Python](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns/inbound-event-loop), [Java](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns/inboundeventloop), [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns/inbound-event-loop), [Rust](https://github.com/superdurable/dex/tree/main/examples/rust/src/control_patterns/inbound_event_loop).

```text
rpc sendReply(text)       -> validate text; if invalid: return a rejection
                             Replies.publish(text); return accepted
rpc resolve(reason)       -> if reason is empty: return a rejection
                             Resolutions.publish(reason)

AwaitInbound.waitFor(pendingResolution)
    if pendingResolution: Until(Replies.AtLeast(1))         // drain before closing
    else: AnyOf(Replies.AtLeast(1), Resolutions.ForOne(), Timer(inactivity))
AwaitInbound.execute(pendingResolution)
    winner = the Condition that fired
    if winner is Replies:
        Transcript.set(Transcript.get() + Replies.conditionResults())
        if pendingResolution: goTo(Resolve, pendingResolution)
        goTo(AwaitInbound, none)   // a new execution of the same Step: a fresh Timer
    if winner is Resolutions:  goTo(Resolve, Resolutions.conditionResults()[0])
    if winner is the Timer:    goTo(CloseInactive)

Resolve.execute(reason)
    forceCompleteIfChannelsEmpty([Replies], else goTo(AwaitInbound, reason))
CloseInactive.execute      // a reply or resolution committed before the empty check reopens the wait
    forceCompleteIfChannelsEmpty([Replies, Resolutions], else goTo(AwaitInbound, none))
```

- The RPC only validates its input and publishes it to a Channel. It writes no Attribute that the waiting Step reads or locks, except the reply window, which may hold the admitted reply, as in [Close a reply window](control-structures.md#close-a-reply-window-with-an-attribute-lock).
- The waiting Step is the only writer of the transcript. It appends every message it consumed, then loops back to itself. The new execution registers a new wait, so the inactivity Timer restarts after each message.
- Execute asks which Condition won; it never infers the winner from an empty result. A message queued when the wait starts wins at once; otherwise the event that commits first wins. A reply or resolution can still commit after the Timer won. The closing Step's ForceCompleteIfChannelsEmpty catches those messages and returns to the waiting Step, which handles them. Express business priority between Channels by their declaration order.
- Declare the reply Channel before the resolution Channel in AnyOf. When several Channel Conditions are ready at once, the first one declared wins, and only the winner's messages are consumed. Replies accepted before the resolution are therefore always recorded first. If the order must differ, drain the pending replies before you resolve.
- A Channel Condition consumes messages only when it wins. A reply that arrives during Execute stays queued for the next execution, so no reply is lost.
- Use `AtLeast(1)` on the reply Channel to take every queued reply in one execution, or `ForOne` to take one at a time.
- Keep the transcript bounded. A long conversation stores its messages in a [sequentially chunked AttributeMap](flow-shapes-and-state.md#sequentially-chunked-attributemap).
- Every closing Step completes with ForceCompleteIfChannelsEmpty on the reply Channel, as in [Drain an external Channel](channels-and-updates.md#drain-an-external-channel). A non-empty reply Channel at the atomic check blocks the close and takes the fallback. The fallback returns to the waiting Step with the resolution in its input; that Step records the remaining replies and moves to Resolve again. The inactivity close also checks the resolution Channel, because a resolution can commit after the Timer won; its fallback reopens the wait. Keep the waiting Step the only consumer of the reply Channel.
- The resolution RPC rejects an empty reason, because an empty pending resolution means "none".
- When the caller must confirm each reply was handled, the waiting Step records the reply's business ID in the same commit and the caller reads that record; see [Confirm that a message was handled](https://docs.superdurable.io/reference/server/runtime-client#confirm-message-handled).

Why not write the transcript from the RPC: the waiting Step must then lock the transcript, and the RPC must lock it too for its read-modify-write. Locks do not queue. Every RPC that arrives while the Step holds the lock, or while another RPC does, is rejected with a lock conflict, and the client must retry. Publishing to a Channel needs no lock, so concurrent replies never conflict.
