# Polling and timer patterns

Part of the [design patterns](../patterns.md), which hold the pseudocode conventions and the runnable examples.

## Polling

Derived from: [WaitFor and Execute](../../../SKILL.md#waitfor-and-execute), [Workers and time bounds](../../../SKILL.md#workers-attempts-and-time-bounds), [Snapshots](../../../SKILL.md#repeated-execution-snapshots-and-locks), [Streams](../../../SKILL.md#streams).

Use Polling whenever a Flow waits for an external system to become ready or finish: a cloud build, a rollout, a provisioning job, a third-party status. Business signals inside Dex (Channels, RPCs) and business timers (TTL, reminders, inactivity) still use WaitFor; they are not polling. Polling has two shapes. Choose one per wait:

| Question | [WaitFor Timer](#polling-with-waitfor-timer) | [Long-running Step heartbeat](#polling-with-long-running-step-heartbeat) |
| --- | --- | --- |
| What is one round? | A new Step execution, recorded in the Flow's history | A heartbeat or Stream frame inside one attempt |
| Cost | Somewhat heavier: records every round in history | Lighter: one Step execution for the whole wait |
| Between rounds | A durable Timer wait; no Worker task slot is held | The attempt holds a Worker task slot |
| Choose it for | Long intervals, seeing every round, Time Travel back to a round | Short intervals, the lightest loop |

The Timer shape records every round in the Flow's history, so each round is visible and Time Travel can move the run back to a round inside its recent history window. The heartbeat shape is lighter. What a Dex Server failover does to a waiting or running round is in the [high availability reference](https://docs.superdurable.io/reference/server/high-availability#lease).

"Not ready yet" is a normal loop branch, not a retry, as the [suggestion](../../../SKILL.md#suggestions) says. A status that an RPC or another Step writes into Dex is not seen by a retry, because retries reuse the first attempt's snapshot.

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

- Bound the loop with a round limit, the Flow timeout, or a watchdog Timer, unless the business wants it to run until stopped; then give it an explicit stop path, such as a stop Channel or a closing RPC. A round number in the input bounds the number of checks, not elapsed time: calls, retries, and queueing add time to each round.
- The polling interval Timer has already fired when Execute begins, so it cannot enforce a deadline while Execute retries. Computing the next round's Timer from a stored deadline does not cover that retrying round. Use a [deadline watchdog](#deadline-watchdog) when the deadline must trigger work during a retry.
- Keep the input small and fixed in size, such as the job ID and round number. Keep accumulated state in Attributes, never in a growing input.
- This shape calls the provider from Execute, where a read gets Execute's retries and failure route and its result commits; see the [Poll from Execute suggestion](../../../SKILL.md#suggestions).
- The retry policy covers transient errors and Worker crashes. Route exhausted retries with the execute failure route.
- Usually prefer the heartbeat shape for an interval of a few seconds or less.

Never:

- Grow the Step input each round.
- Start a new Flow for each round or page.

#### Deadline watchdog

Derived from: [Timers](../../../SKILL.md#timers), [Time bounds](../../../SKILL.md#workers-attempts-and-time-bounds), [Cancellation](../../../SKILL.md#flow-timeout-completion-and-cancellation), [Joins](../../../SKILL.md#joins).

Use it only when work must happen at a deadline shorter than the Flow, such as a notice or a withdrawal, while work, such as a polling loop or a retrying Step, is still running. When an overrun past the retry budget is acceptable, the retry total duration alone is enough: with Workers promptly available the overrun is about one method timeout, and queueing can make it longer. The watchdog requests the deadline action independently of the retrying method. Timers may fire late, and cancellation cannot undo or forcibly stop external calls, so a strict external cutoff needs a deadline check or fence at the receiver. The work runs as the only branch of a Join, and the join Step races the branch's result against a Timer.

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

- Keep the longest gap between heartbeats and Stream writes, including the slowest healthy call and the interval, below the Step's [heartbeat timeout](../../../SKILL.md#workers-attempts-and-time-bounds), with a margin. Usually keep the default heartbeat timeout when the loop fits it, so a stalled Worker is detected quickly; raise it only when a healthy call is silent longer.
- A business deadline that is shorter than the whole wait, or that the loop's Step options cannot hold because the Step re-enters itself to commit each status change, uses the [deadline watchdog](#deadline-watchdog): the waiting Step is the Join's branch, and the join Step races its result against a Timer.
- The retry policy covers Worker crashes and transient errors. Use a few attempts with backoff, and set the total duration to the maximum wait. Never use a large attempt count as a time bound.
- The total duration is the retry budget. It is checked after an attempt fails, before scheduling another retry. An already scheduled retry may wait past the budget in the queue and still run for a full method timeout. With Workers promptly available, the whole wait lasts about the total duration plus one method timeout; queueing can make it longer. When that overrun is acceptable, the Step code computes no deadline of its own, and neither the checkpoint nor the input carries one. Otherwise use the [deadline watchdog](#deadline-watchdog).
- Use the language's plain sleep. Pass the cancellation signal to provider calls that accept it, as the [suggestion](../../../SKILL.md#suggestions) says; the loop exits at the next boundary that can report it. See [Make the Step stop](https://docs.superdurable.io/reference/server/runtime-data#cancel-honour).
- The checkpoint is only resume state, such as the last reported status. Resuming from the last stored checkpoint reduces repeated progress frames; it can be lost, so consumers tolerate duplicates and gaps. See [Heartbeat](https://docs.superdurable.io/reference/go/step-options#heartbeat).
- A business condition that is not a fixed duration, such as a lease the Flow does not own expiring, is still checked inside the loop.
- Write Stream frames only on status changes. Consumers recover from Attributes or a read-only RPC.
- Do not hold, for the whole wait, a lock that an RPC takes or that other Steps must use meanwhile: the Steps queue until the loop ends, and the RPCs fail. Commit locked results in a short following Step.
- The recovery error does not carry the last status. The Stream and logs do. To branch on which limit ended the wait, follow [recovery error types](../error-handling.md#recovery-error-types).
- Either durability works. With ASYNC, work whose result was not yet saved can run again; the checkpoint is only a resume hint.

Never:

- Stay silent longer than the Step's heartbeat timeout.
- Hand-write a deadline that duplicates the method timeout or the retry total duration.

To page through results, keep paging inside the attempt and checkpoint the page token in the heartbeat; to give each page its own recovery boundary, GoTo the same Step with the small token as input.

## Durable timers

A Timer is a durable Condition, not an in-process timer. A skipped Timer acts as if it fired.

### Cron schedule

Derived from: [Timers](../../../SKILL.md#timers), [Conditions](../../../SKILL.md#conditions-and-channel-messages), the [Must](../../../SKILL.md#must).

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
- Key each run's external effect by its occurrence ([Must](../../../SKILL.md#must)).

### Reminder

Derived from: [Timers](../../../SKILL.md#timers), [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks), the [Must](../../../SKILL.md#must).

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
- Parallel waiters each need their own stop message, such as one per branch on a ChannelMap. A stop Attribute records the fact but wakes no waiter, because WaitFor has no Attribute Condition.
- Key each send by a stable occurrence ID that the receiver recognizes and deduplicates ([Must](../../../SKILL.md#must)).
- The runnable reminders examples show the single-consumer relative-Timer loop. Their send call has no occurrence idempotency key.

### Idle Timer

Derived from: [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Timers](../../../SKILL.md#timers).

Act only after a period with no activity. Go folder: `inactiveness-tracker-timer`.

```text
Track.waitFor -> AnyOf(Timer(window), Activity.ForOne())
Track.execute -> if the Timer fired: goTo(ProcessInactivity)
                 else: goTo(Track)          // a fresh Timer after each activity
rpc recordActivity -> Activity.publish()
```

After each activity, the next WaitFor starts a full Timer(window). Computing the rest of the window from a stored timestamp also works; a fresh Timer is less code and skippable in tests.

Usually keep the deadline in the Flow where the activity happens: a separate Flow needs a call on every operation. Cross-Flow calls add network and coordination cost and repeat when the calling method retries, and each activity message adds durable work and history to the target. See the [Flow boundaries suggestion](../../../SKILL.md#suggestions).

### Idle Timer with Resolution

Derived from: [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Timers](../../../SKILL.md#timers), [Flow completion](../../../SKILL.md#flow-timeout-completion-and-cancellation), [RPCs](../../../SKILL.md#rpcs).

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

- The RPC only validates its input and publishes it to a Channel. It writes no Attribute that the waiting Step reads or locks, except the reply window, which may hold the admitted reply, as in [Close a reply window](control-structures.md#close-a-reply-window-with-an-attribute-lock). Publishing needs no lock, so concurrent replies never hit a lock conflict, as they would if each RPC locked and rewrote the transcript.
- The waiting Step is the only writer of the transcript. It appends every message it consumed, then loops back to itself. The new execution registers a new wait, so the inactivity Timer restarts after each message.
- Execute asks which Condition won; it never infers the winner from an empty result. A reply or resolution can still commit after the Timer won; the closing Step's ForceCompleteIfChannelsEmpty catches it and returns to the waiting Step, which handles it.
- Declare the reply Channel before the resolution Channel in AnyOf, so when both are ready in one check, replies accepted before the resolution are recorded first ([AnyOf order](../../../SKILL.md#conditions-and-channel-messages)). If the order must differ, drain the pending replies before you resolve.
- Use `AtLeast(1)` on the reply Channel to take every queued reply in one execution, or `ForOne` to take one at a time.
- Keep the transcript bounded. A long conversation stores its messages in a [sequentially chunked AttributeMap](flow-shapes-and-state.md#sequentially-chunked-attributemap).
- Every closing Step completes with ForceCompleteIfChannelsEmpty on the reply Channel, never a size check first, as in [Drain an external Channel](channels-and-updates.md#drain-an-external-channel). A non-empty reply Channel at the atomic check blocks the close and takes the fallback. The fallback returns to the waiting Step with the resolution in its input; that Step records the remaining replies and moves to Resolve again. The inactivity close also checks the resolution Channel, because a resolution can commit after the Timer won; its fallback reopens the wait. Keep the waiting Step the only consumer of the reply Channel.
- The resolution RPC rejects an empty reason, because an empty pending resolution means "none".
- When the caller must confirm each reply was handled, the waiting Step records the reply's business ID in the same commit and the caller reads that record; see [Confirm that a message was handled](https://docs.superdurable.io/reference/server/runtime-client#confirm-message-handled).
