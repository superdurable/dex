# Step, Wait, decision, and StepOptions

A Step is one unit of durable work and one state transition in a Flow. It has two methods:

- **WaitFor** returns a Wait: the Conditions under which the Step may run.
- **Execute** performs the work and returns a Step decision: the next Steps, or how the Flow closes.

Either method may be omitted where the SDK allows it. A Step without WaitFor runs Execute at once.

## Commit boundaries

Each WaitFor call, each Execute call, and each RPC call is a separate commit boundary.

- Dex stages the Attribute writes, Channel publications, and pending-message deletes a method makes. It commits them together with the method's Wait or decision, only after the method returns successfully.
- If the method fails, none of its staged changes commit.
- WaitFor and Execute never share a commit.
- External API calls are outside this boundary. Dex cannot roll them back.
- Heartbeat checkpoints and Stream messages sent before the result are not part of the commit.
- Hard-delete Attribute writes are the exception: they are stored before the commit. See [attribute.md](attribute.md#hard-delete-attributes).

## WaitFor versus Execute

Use Execute for work and WaitFor for durable waiting. One rule holds in every SDK:

- WaitFor declares the Wait. It may write the Dex state needed to establish the wait: Attributes, Channel publications, and Step execution local values.
- WaitFor never calls an external system. No provider query, no provider mutation, and no irreversible side effect. Put every provider call in Execute, and keep it idempotent or compensatable there.
- WaitFor may run more than once, because a failed WaitFor retries. Keep it cheap, deterministic, and safe to repeat.
- Never poll a provider from WaitFor or with a Timer loop. Waiting for an external system uses the [Polling pattern](patterns.md#polling): one long-running Execute that loops and heartbeats.
- Never use the Client from WaitFor to start or mutate another Flow. To wait on a child Flow, return a SubFlow Condition.
- To pass a value that WaitFor computed to Execute of the same Step execution, use a Step execution local value. See [Step execution local values](#step-execution-local-values).

### Step execution local values

- A Step execution local value is scoped to one Step execution. Only the WaitFor and Execute calls of that execution can read it. Other Steps, RPCs, and Clients cannot.
- It is durable. Dex commits it with the result of the method that wrote it and stores it in the Step execution record. A later call of the same execution reads it even on another Worker or after a Worker restart.
- A write from a failed call is discarded with the call's other staged changes.
- The value is encoded with the payload codec, like an Attribute value. Keep it small.
- Do not copy Step input into it, and do not use it for state another Step or a Client needs. Use an Attribute for that.

## Split Steps by recovery boundary

Split a provider action into its own Step when its success deserves its own checkpoint, retry and timeout policy, failure route, or audit boundary. An idempotent queue send often merits its own Step, so a later failure does not resend it.

Do not split only because there is another API call. Keep consecutive work in one Step when it shares one recovery boundary.

## Waits and Conditions

| Wait | Proceeds when |
| --- | --- |
| Until(c) | the one Condition is met |
| AllOf(c...) | every Condition is met |
| AnyOf(c...) | at least one Condition is met |
| AnyCombinationOf(groups...) | every Condition in at least one group is met (an OR of AND groups) |
| SkipWaitImmediately | at once; for a dynamic WaitFor branch that needs no wait |

| Condition | Meaning |
| --- | --- |
| Timer | a durable delay. See [timer.md](timer.md). |
| Channel | enough pending messages on a Channel or ChannelMap instance. See [channel.md](channel.md#wait-and-consume). |
| SubFlow | a child Flow finishes. WaitFor starts the child. See [subflow.md](subflow.md). |

Rules:

- Leave Conditions unnamed in Until, AnyOf, and AllOf. Read Channel results from the Channel definition and Timer outcomes from the Context.
- Every Condition in AnyCombinationOf needs a unique Condition ID.
- A Timer needs an ID when another operation selects it by ID. SkipTimer can also select by zero-based Condition index.
- When several AnyOf alternatives are ready together, Dex picks the first in canonical order: Timers in declaration order, then Channels, then SubFlows. An unready earlier Condition does not block a later ready one. Mixed kinds do not keep one global argument order.
- Consumption is not greedy. Only the winning AnyOf alternative, or the winning AnyCombinationOf group, consumes Channel messages. The others stay pending.
- AnyOf is a race. For strict priority, return only the high-priority Condition until it resolves.
- In Execute, check which Condition won before reading its result. When a Timer wins, no Channel message was consumed.

## Step decisions

| Decision | Effect |
| --- | --- |
| GoTo | Start one next Step with typed input. |
| GoToMany | Start several next Steps, each with its own input and optional StepOptions. This is how a Flow runs work in parallel. |
| GracefulComplete | End this branch and request completion. The Flow completes when every branch has stopped. |
| DeadEnd | End this branch only. A Flow with no active Steps keeps running, for example while RPCs can still move it. |
| ForceComplete | Complete the Flow at once and abandon other active Steps. |
| ForceFail | Fail the Flow at once with a reason. |
| ForceCompleteIfChannelsEmpty | Atomically check the named Channels. If all are empty, complete; otherwise take the supplied movement. |

- GracefulComplete and ForceComplete can carry output. The Flow result lists each output with its Step type and Step execution ID.
- A Channel stays non-empty for ForceCompleteIfChannelsEmpty while a consumed message's Step has not finished Execute.
- A decision can also cancel active Steps: by Step type, or the siblings started by the same parent Step execution. Use cancellation deliberately, for example after a first-winner branch.
- Dex selects the Steps to cancel after the decision commits. Steps that the same decision starts are never canceled by it, so one decision can cancel a Step type and start a fresh execution of it.
- Cancellation is cooperative. The SDK signals it to the handler, and Dex discards the canceled Step's later result, writes, retries, and recovery routes. A handler that ignores the signal keeps running and its external calls still take effect.
- A movement can override StepOptions for that one Step execution.

## Durability: SYNC and ASYNC

Durability resolves in this order: the WaitFor or Execute override in StepOptions, then the Flow configuration default, then **SYNC**. WaitFor and Execute resolve independently.

| | SYNC | ASYNC |
| --- | --- | --- |
| Attempt start | persisted before the Worker receives the task | task handed out first; start persisted in a later batch |
| Result | persisted before the Flow advances | Flow advances first; commit is batched shortly after |
| Server node crash | no lost result | a recent result is lost; the method and the work after it run again |
| Crash before the start is persisted | not possible | the same attempt number runs again, consuming no retry; the two runs can overlap and see different input |
| Recovery attempt progress | can report NOT_STARTED | always NOT_COMPLETED |
| Latency | higher | lower; a single Flow runs faster |
| Throughput | about the same | about the same |

Choose:

- **ASYNC** as the Flow default when most handlers are short and idempotent. Do not choose it for throughput.
- **SYNC** as a method override when a replay would be costly or unsafe: a non-idempotent external effect, an expensive call such as an LLM generation, or a long method. For a long method the commit latency saved is negligible, while a replay restarts the call.
- A long method that checkpoints its progress, such as a polling loop, can inherit ASYNC.
- SYNC for any method whose recovery relies on NOT_STARTED.

Both modes run the same attempts. Retry policy, method timeout, heartbeat timeout, and checkpoints apply in both. Do not describe ASYNC as a separate local execution.

## Attempts

A method call is one logical execution. Its attempts share the maximum attempts, the retry total duration, and 1-based attempt numbers.

- An attempt starts when Dex hands it to a Worker. Time spent waiting for locks or in the task queue does not count.
- A Step that no Worker takes waits in a durable task queue. It does not fail and consumes no retry. Only a Flow timeout or a stop ends that wait. Steps are dispatched roughly in ready order, not strictly.
- Dex never hands the same attempt number to a second Worker. A Worker crash or disconnect after hand-out fails the attempt as possibly run and consumes a retry, in both modes.
- An attempt goes back to the queue without consuming a retry only when the Worker certainly never ran it: an unregistered Step type, a task never delivered, or a graceful Worker shutdown.
- Attempts of one Step can overlap in time, because cancellation is cooperative. Keep handlers idempotent.
- Dex sends cancellation to the Worker when a Step is canceled by a decision, an RPC, a stop, Flow close, or time travel. The handler sees it through the SDK's cancellation signal.

### Retry snapshots and locks

- Dex takes the state snapshot when it hands the first attempt to a Worker. Every retry of that method call reuses the first snapshot, including its selections, and keeps its Attribute locks.
- A retry does not see writes made after that snapshot. Do not retry a Step to wait for an RPC or another Step to fix state. Have the writer publish a Channel message and wait on it in the next Step's WaitFor, or fail to a recovery route.
- Locks stay held through retry backoff and are released when the method call ends. A writing RPC that needs the lock gets a lock conflict for that whole time. Keep lock scope narrow and the retry budget bounded.
- A returned or retrying attempt keeps its locks while queued. If no Worker can run it, it holds them until the Flow times out or stops.
- An attempt returned without running keeps its snapshot and may run much later. Locked values cannot change meanwhile, but unlocked values may be stale.
- A first attempt not yet handed out holds no locks, but later Steps that need the same lock wait behind it.

### Time bounds

| Bound | Meaning |
| --- | --- |
| Method timeout | Bounds one attempt, from hand-out. The only bound on a single attempt. |
| Heartbeat timeout | Fails an Execute attempt that stays silent longer. Detects a stalled Worker sooner than the method timeout. |
| Retry total duration | Starts at the first hand-out. Checked only after an attempt fails, to decide whether to retry. It never ends a running attempt, so the last attempt can end after it. Queue time after backoff counts toward it. |
| Attempt deadline | The absolute end of the current attempt, supplied by the Server on the Context. |

- To bound one long attempt, set the method timeout, not the retry total duration.
- To bound a long wait, set the method timeout and the retry total duration to the same maximum, with a few attempts and backoff.
- Always set the retry total duration explicitly for a long-running Step, because its default is long.
- Use the attempt deadline to size an external call's timeout and to decide whether another unit of work fits. Check for absence first: some tasks have no deadline. Do not store it in a checkpoint or Step input, and do not recompute it from the method timeout.
- When an external call needs a stricter deadline, wrap it in a child context or the SDK's cancellation mechanism.
- Do not infer durability from a large attempt timeout. The timeout is a safety bound.

### Heartbeats and checkpoints

- A long-running Execute must record a heartbeat or write a Stream message before its heartbeat timeout. A Stream message is an implicit heartbeat.
- A heartbeat value is a retry checkpoint. The next attempt receives the last stored value.
- An explicit valueless heartbeat clears the checkpoint. A Stream message keeps the current one.
- Heartbeats are not acknowledged. A returned heartbeat call does not prove Dex stored it, even for SYNC. Checkpoints are batched; the next attempt may get an older value. Never use a checkpoint as the only record of an external effect.
- The default heartbeat timeout is short. A non-streaming provider call, such as a long LLM generation, is silent for its whole duration and fails a healthy attempt. Raise the heartbeat timeout to the longest acceptable silent interval, at least the method timeout for one silent call. Do not add periodic heartbeats only to mask provider silence: they prove only that the Worker runs.
- Act on cancellation promptly. Dex cancels a handler that exceeds its heartbeat timeout even if it records none.

### Retry and failure policy

- Retry policy re-runs the same method on the same Step execution, with backoff.
- RetryAfter from a handler overrides the next retry delay. It is for recovery from a failed attempt, never a loop for waiting on an external system.
- After WaitFor retries are exhausted: fail the Flow (default), or proceed to the same Step's Execute with unchanged input. Execute then sees that WaitFor failed and can read the recovery error.
- After Execute retries are exhausted: fail the Flow (default), or proceed to a registered recovery Step with the same input. The recovery Step reads the recovery error.
- While retries are still running, the Context exposes the latest attempt's failure. Failure policy decides only what happens after retries end.
- Read [error-handling.md](error-handling.md#recovery-error-types) before branching on the recovery error.

A Flow may register several instances of one Step class under different Step types, such as a generic Step parameterized by input. Give each instance a distinct Step type. Where a decision, failure route, or cancellation can target a class, that target is valid only when exactly one instance of the class is registered; otherwise target the instance, or a Step reference that holds only the Step type.

## State selection and locks

Every WaitFor and Execute call receives ordinary Attributes and Channel size metadata automatically.

- AttributeMap values and pending Channel messages require selections in StepOptions. Select exact instances when the keys are known. Select the whole map only to enumerate it.
- Reading unselected AttributeMap or pending-message state raises a not-loaded error. A selected empty Channel returns an empty list.
- Size and map-key introspection need no load.
- WaitFor and Execute load independent snapshots. Execute loads after the Wait consumed its messages.
- WaitFor and Execute have separate lock sets.
- An Attribute lock does not load anything. A load does not lock anything.
- Lock the exact AttributeMap instance for a read-modify-write that can race. For a whole-map invariant, see [attribute.md](attribute.md#locks).

## Context

The Step Context exposes, among others:

- the Flow ID, Run ID, Step execution ID, and attempt number;
- the Flow run's start time. It is the same for every Step, retry, timeout handler, and RPC in one run. Derive deterministic identities from the Flow ID plus that time instead of the wall clock, which differs per retry. It does not identify a run across Flow ID reuse alone.
- the last heartbeat value;
- the recovery error and whether WaitFor failed;
- Timer and SubFlow results of the Wait;
- the attempt deadline and the cancellation signal;
- Attribute, AttributeMap, and Channel access for the loaded snapshot.

## Configure the complete method policy

For every WaitFor and Execute method, decide:

- durability override or deliberate inheritance;
- method timeout;
- retry attempts, backoff, and total duration;
- selective AttributeMap and Channel loads;
- Attribute or map-instance locks;
- the exhausted-retry failure route;
- for Execute, the heartbeat timeout.

Exact option names and defaults are in the language's Developer reference.

## External effects

- Key each external effect by an ID that is stable across attempts, such as the Flow ID plus the Step execution ID, or a business key. Send it to the target service.
- On a possibly-run failure, reconcile at the target by that key before you repeat or report.
- Record the outcome in the Flow.
- A single-attempt Step is at-most-once only within these limits.
- Never keep correctness state only in Worker memory.

## Verify

- A short ASYNC method, and a retried ASYNC method that resumes from its checkpoint.
- A long method that runs as SYNC.
- Retry attempts and the total-duration budget across retries.
- The external call's child deadline.
- Worker replacement at durable waits and at external-effect boundaries.
- ASYNC methods and the work after them tolerate a replay.
- A handler that exceeds its heartbeat timeout is retried.
