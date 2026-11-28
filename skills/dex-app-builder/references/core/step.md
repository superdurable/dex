# Step, Wait, decision, and StepOptions

A Step is one unit of durable work and one state transition in a Flow. It has two methods:

- **WaitFor** returns a Wait: the Conditions under which the Step may run.
- **Execute** performs the work and returns a Step decision: the next Steps, or how the Flow closes.

Either method may be omitted where the SDK allows it. A Step without WaitFor runs Execute at once.

## Commit boundaries

Each WaitFor call, each Execute call, and each RPC call is a separate commit boundary.

- Dex stages the Attribute writes, Channel publications, and pending-message deletes a method makes. It commits them together with the method's Wait or decision, only after the method returns successfully.
- If the method fails, none of its staged changes commit.
- A commit is atomic but not isolated: two methods that read and then write the same value lose an update unless both declare the same Attribute lock. See [Commits and isolation](../../SKILL.md#commits-and-isolation).
- WaitFor and Execute never share a commit.
- External API calls are outside this boundary. Dex cannot roll them back.
- Heartbeat checkpoints and Stream messages sent before the result are not part of the commit.
- Hard-delete Attribute writes are the exception: they are stored before the commit. See [attribute.md](attribute.md#hard-delete-attributes).

## WaitFor versus Execute

Use Execute for work and WaitFor for durable waiting. WaitFor's facts are in [WaitFor and Execute](../../SKILL.md#waitfor-and-execute):

- WaitFor returns the Conditions to wait on. Its result can also write Attributes, publish and delete Channel messages, and write Step execution local values; they commit with the Wait.
- WaitFor has no failure route or compensation point of its own, and Dex Web shows it as the wait. So usually put calls that can fail, and every provider mutation, including Connector write operations, in Execute, and keep each idempotent or compensatable there; see [WaitFor sets up the wait](../../SKILL.md#suggestions).
- WaitFor may make read-only, idempotent calls, for example to read a due date that sets a Timer. The Timer is durable once WaitFor's result commits.
- When a WaitFor read can fail permanently, let Execute route on it: either record the error in a [Step execution local value](#step-execution-local-values) and return a Wait that is ready immediately, such as Go's SkipWaitImmediately, or use the WaitFor failure policy that proceeds to Execute. A permanent error left to Step retry only delays the outcome until the retry budget runs out.
- WaitFor may run more than once: a retryable error follows WaitFor's own retry policy, and when that budget runs out, the WaitFor failure policy decides. Keep it cheap and safe to repeat. A retry may read a different value; the Wait that commits is the one that counts.
- A read in Execute gets Execute's retries and failure route, and its result commits. So waiting for an external system usually uses one of the two [Polling](patterns/polling-and-timers.md#polling) shapes, not a poll from WaitFor: a WaitFor Timer with one provider call per Execute and a GoTo to the same Step, or one long-running Execute that loops and heartbeats.
- Starting or mutating another Flow through the Client is an external call; usually make it from Execute, as above. To wait on a child Flow, return a SubFlow Condition.
- To pass a value that WaitFor computed to Execute of the same Step execution, use a Step execution local value. See [Step execution local values](#step-execution-local-values).

### Step execution local values

- A Step execution local value is scoped to one Step execution. Only the WaitFor and Execute calls of that execution can read it. Other Steps, RPCs, and Clients cannot.
- It is durable. Dex commits it with the result of the method that wrote it and stores it in the Step execution record. A later call of the same execution reads it even on another Worker or after a Worker restart.
- A write from a failed call is discarded with the call's other staged changes.
- The value is encoded with the payload codec, like an Attribute value. Keep it small.
- Other Steps and Clients cannot read it, so state they need goes in an Attribute. Step input is already available to both methods.

## Split Steps by recovery boundary

Usually split a provider action into its own Step when its success deserves its own checkpoint, retry and timeout policy, failure route, or audit boundary. An idempotent queue send often merits its own Step, so a later failure does not resend it.

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
| Channel | enough pending messages on a Channel or ChannelMap instance. See [channel.md](channel.md#wait-and-consume). A Join's conditions are Channel conditions on its ChannelMap; see [patterns.md](patterns/parallel.md#await-parallel-steps). |
| SubFlow | a child Flow finishes. WaitFor starts the child. See [subflow.md](subflow.md). |
| Graceful completion | the Flow has a graceful completion request, from a GracefulComplete decision or a Flow stop with graceful complete. See [Graceful completion requests](https://docs.superdurable.io/reference/go/waits#graceful-completion-requests). |

Rules:

- Conditions in Until, AnyOf, and AllOf need no names. Read Channel results from the Channel definition and Timer outcomes from the Context.
- Every Condition in AnyCombinationOf needs a unique Condition ID.
- A Timer needs an ID when another operation selects it by ID. SkipTimer can also select by zero-based Condition index.
- Dex checks a wait when it starts and after each committed change that can satisfy it. Messages queued before the wait starts satisfy it at once; otherwise the first event to commit wins. Only when several Conditions are ready in the same check does the AnyOf order apply: Timer, then Channel, then SubFlow, then graceful completion, and declaration order within one kind. That is rare, so the first committed event usually decides; see [Conditions and Channel messages](../../SKILL.md#conditions-and-channel-messages). Do not build business priority between a Timer and a Channel on the selection order.
- Consumption is not greedy. Only the winning AnyOf alternative, or the winning AnyCombinationOf group, consumes Channel messages. The others stay pending.
- AnyOf is a race. For strict priority, return only the high-priority Condition until it resolves. For a business priority between events, state which definition of "the same moment" applies; see [patterns.md](patterns/control-structures.md#arbitrate-by-business-priority).
- In Execute, check which Condition won before reading its result. When a Timer wins, no Channel message was consumed, and a message can still commit before Execute runs. When that matters, close a reply window with an Attribute lock; see [patterns.md](patterns/control-structures.md#close-a-reply-window-with-an-attribute-lock).
- When the Flow can receive a graceful completion request and a Step that would otherwise wait indefinitely, such as one that waits for the next message, must end then, add the graceful completion Condition. Combine it with that Channel in AnyOf, so the Step stops waiting when the Flow is asked to complete gracefully.
- Handle consumed messages first in Execute; return DeadEnd only when no message was consumed and the graceful completion request exists, so the Flow completes once no Step is active. See [Wait for a completion request](https://docs.superdurable.io/reference/server/step-and-flow-close#graceful-completion-condition).

## Step decisions

| Decision | Effect |
| --- | --- |
| GoTo | Start one next Step with typed input. |
| GoToMany | Start several next Steps, each with its own input and optional StepOptions. This is how a Flow runs work in parallel. |
| GracefulComplete | End this branch and request completion. Dex begins to complete the Flow when every branch has stopped. |
| DeadEnd | End this branch only. Without a pending GracefulComplete, a Flow with no active Steps keeps running, for example while RPCs can still move it. |
| ForceComplete | Complete the Flow at once and abandon other active Steps. |
| ForceFail | Fail the Flow at once with a reason. |
| ForceCompleteIfChannelsEmpty | Atomically check the named Channels. If all are empty, complete; otherwise take the supplied movement. |

- A GracefulComplete request stays in force until Dex completes the Flow. A later DeadEnd does not withdraw it. Edge cases are in the [Step and Flow close reference](https://docs.superdurable.io/reference/server/step-and-flow-close#completion-requests).
- Dex never closes a Flow because no Step is active. Without a pending GracefulComplete, such a Flow keeps running until something else closes it, such as its Flow timeout, a stop, or a Step that an RPC starts. When the Flow should close after its last branch, end that branch with a completing decision. A Flow that must not wait forever needs a Flow timeout.
- GracefulComplete and ForceComplete can carry output. The Flow result lists each output with its Step type and Step execution ID.
- Keep data that must survive a failure in an Attribute, not in an output. A Flow that does not complete has no outputs; see [Flow outputs](https://docs.superdurable.io/reference/server/step-and-flow-close#flow-outputs).
- A decision can also cancel active Steps: by Step type, or the siblings started by the same parent Step execution, for example after a first-winner branch.
- Dex selects the Steps to cancel after the decision commits. Steps that the same decision starts are never canceled by it, so one decision can cancel a Step type and start a fresh execution of it.
- A canceled Step's consumed Channel messages normally return to the front of their Channels, so a message can reach Execute twice. Keep Execute idempotent. When Dex drops them instead, see [the reference](https://docs.superdurable.io/reference/server/runtime-data#canceled-step-messages).
- Cancellation is cooperative. The SDK signals it to the handler, and the handler must check the signal and return. Dex discards the canceled Step's later result. See [Step cancellation](https://docs.superdurable.io/reference/server/step-and-flow-close#step-cancellation).
- Cancellation discards only what the current method has not committed. What the Step's earlier methods committed, such as its WaitFor writes, stays.
- A movement can override StepOptions for that one Step execution.

## Durability: SYNC and ASYNC

Durability resolves in this order: the WaitFor or Execute override in StepOptions, then the Flow configuration default, then **SYNC**. WaitFor and Execute resolve independently.

| | SYNC | ASYNC |
| --- | --- | --- |
| Attempt start | persisted before the Worker receives the task | task handed out first; start persisted in a later batch |
| Result | persisted before the Flow advances | Flow advances first; commit is batched shortly after |
| Server node crash | no lost result | a recent result is lost; the method and the work after it run again |
| Recovery attempt progress | can report NOT_STARTED | always NOT_COMPLETED |
| Latency | higher | lower; a single Flow runs faster |
| Throughput | about the same | about the same |

Usually choose:

- **ASYNC** as the Flow default when most handlers are short and idempotent. It does not raise throughput.
- **SYNC** as a method override when a replay would be costly or unsafe: a non-idempotent external effect, an expensive call such as an LLM generation, or a long method. For a long method the commit latency saved is negligible, while a replay restarts the call.
- A long method that checkpoints its progress, such as a polling loop, can inherit ASYNC.
- SYNC for any method whose recovery relies on NOT_STARTED; only SYNC can report it.

Both modes run the same attempts. Retry policy, method timeout, heartbeat timeout, and checkpoints apply in both.

## Attempts

A method call is one logical execution. Its attempts share the maximum attempts, the retry total duration, and 1-based attempt numbers.

- An attempt starts when Dex hands it to a Worker. Time spent waiting for locks or in the task queue does not count.
- A Step that no Worker takes waits in a durable task queue. It does not fail and consumes no retry. Only a Flow timeout or a stop ends that wait.
- Do not rely on the order in which ready Steps start. Use a Channel or an Attribute lock when order matters.
- Dex never hands the same attempt number to a second Worker. A Worker crash or disconnect after hand-out fails the attempt as possibly run and consumes a retry, in both modes.
- An attempt goes back to the queue without consuming a retry only when the Worker certainly never ran it: an unregistered Step type, a task never delivered, or a graceful Worker shutdown.
- Attempts of one Step can overlap in time, because cancellation is cooperative. Keep handlers idempotent.
- Dex sends cancellation to the Worker when a Step is canceled by a decision, an RPC, a stop, Flow close, or time travel. The handler sees it through the SDK's cancellation signal.

### Retry snapshots and locks

- Dex takes the state snapshot when it hands the first attempt to a Worker. Every retry of that method call reuses the first snapshot, including its selections, and keeps its Attribute locks.
- A retry does not see writes made after that snapshot. Do not retry a Step to wait for an RPC or another Step to fix state. Have the writer publish a Channel message and wait on it in the next Step's WaitFor, or fail to a recovery route.
- Locks stay held through retry backoff and while a retrying attempt waits for a Worker, and are released when the method call ends. A writing RPC that needs the lock gets a lock conflict for that whole time, and under a lock that only Steps share, the other Steps wait for that whole time. So under a lock that an RPC takes, usually make no external call; see [locks](attribute.md#locks).

### Time bounds

| Bound | Meaning |
| --- | --- |
| Method timeout | Sets the total time limit of one attempt, from hand-out. |
| Heartbeat timeout | Can end a WaitFor or Execute attempt earlier when it makes no progress: no heartbeat or Dex Stream write for that long. Detects a stalled Worker sooner than the method timeout. |
| Retry total duration | The retry budget. Starts at the first hand-out. Checked after an attempt fails, to decide whether to retry. |
| Attempt deadline | The absolute end of the current attempt, supplied by the Server on the Context. |

- To bound one long attempt, set the method timeout, not the retry total duration.
- To bound a long wait, set the method timeout and the retry total duration to the same maximum, with a few attempts and backoff. A deadline for the whole Flow uses the Flow timeout. A deadline for one wait uses a durable Timer Condition, as in the [deadline watchdog](patterns/polling-and-timers.md#deadline-watchdog).
- The retry total duration counts from the first delivery. It is checked after an attempt fails, before scheduling another retry, and retrying can stop up to one backoff interval early. An already scheduled retry may wait past the budget in the queue and still run for a full method timeout. With Workers promptly available, the overrun is about one method timeout; queueing can make it longer. See [Workers, attempts, and time bounds](../../SKILL.md#workers-attempts-and-time-bounds). When that overrun is acceptable, "keep trying for up to X" is the retry total duration plus an Execute failure route that proceeds to a Step that handles the failure, and the Step needs no deadline guard of its own. When work must happen at the deadline, such as a notice or a withdrawal, use a [deadline watchdog](patterns/polling-and-timers.md#deadline-watchdog): it requests the deadline action independently of the retrying method. Timers may fire late, and cancellation cannot undo or forcibly stop external calls, so a strict external cutoff needs a deadline check or fence at the receiver.
- Check the default retry total duration, 4 hours, against the method timeout. Set it when attempts are long enough that the default would allow fewer retries than you want, or when retries must stop sooner. To keep retries inside a wait window, use the rule above. To allow several near-full-length attempts, size it at about maximum attempts times the method timeout, plus the backoff between them.
- Dex ends a hung attempt through the heartbeat timeout and the method timeout, sends cancellation to the handler, and retries by the retry policy. Pass the cancellation signal into each call that accepts one. A call that cannot observe cancellation keeps its Worker slot until it returns, even after Dex ends the attempt, so usually give such a call its own timeout. The method or heartbeat timeout still bounds the attempt, and Dex schedules later attempts by the retry policy. Add a tighter call timeout also when the business needs a bound shorter than the method timeout.
- When a call needs its own timeout, use the attempt deadline to size it and to decide whether another unit of work fits. Check for absence first: some tasks have no deadline. Do not store it in a checkpoint or Step input, and do not recompute it from the method timeout. Wrap the call in a child context or the SDK's cancellation mechanism.

### Heartbeats and checkpoints

- A long-running Execute must record a heartbeat or write a Stream message before its heartbeat timeout. A Stream message is an implicit heartbeat.
- A heartbeat value is a retry checkpoint. The next attempt receives the last stored value.
- An explicit valueless heartbeat clears the checkpoint. A Stream message keeps the current one.
- A checkpoint is resume state and may be older than the last heartbeat. Key each external effect by a stable ID, never by the checkpoint alone.
- The default heartbeat timeout is 1 minute; see each SDK's StepOptions reference, such as [Go](https://docs.superdurable.io/reference/go/step-options#defaults). If the attempt reaches its heartbeat deadline without a heartbeat or a Dex Stream write, Dex times out the attempt; raising only the method timeout keeps that deadline. A non-streaming provider call, such as a long LLM generation, sends neither for its whole duration. Either raise the heartbeat timeout above the longest acceptable silent interval, or heartbeat from a background task while the call runs. A background heartbeat proves only that the Worker runs, so a hung call then ends at the method timeout.
- A loop of short calls that heartbeats between them, such as the [Polling pattern](patterns/polling-and-timers.md#polling), keeps the longest gap between heartbeats, including the slowest healthy call and the interval, below the heartbeat timeout, with a margin. Check that against the default before keeping it. The defaults are in each SDK's StepOptions reference, such as [Go](https://docs.superdurable.io/reference/go/step-options#defaults).
- Act on cancellation promptly. Dex cancels a handler that exceeds its heartbeat timeout even if it records none.

### Retry and failure policy

- Retry policy re-runs the same method on the same Step execution, with backoff. It is for errors that can succeed on a later attempt.
- A returned error is retried until the budget runs out, even when the provider confirmed it is permanent. So when a provider confirms a permanent error, Execute usually routes to a failure-handling Step or returns ForceFail; see [Route confirmed permanent errors](../../SKILL.md#suggestions). An error that an operator can fix while retries run, such as a missing credential scope, may stay in retry.
- RetryAfter from a handler overrides the next retry delay. It is for recovery from a failed attempt; for "not ready yet", a normal GoTo loop usually fits better, because retries mix not-ready with real failures and spend the retry budget.
- After WaitFor retries are exhausted: fail the Flow (default), or proceed to the same Step's Execute with unchanged input. Execute then sees that WaitFor failed and can read the recovery error.
- After Execute retries are exhausted: fail the Flow (default), or proceed to a registered recovery Step with the same input. The recovery Step receives the failed Step's input unchanged, and it also gets the failure through the recovery error on the Context.
- While retries are still running, the Context exposes the latest attempt's failure. Failure policy decides only what happens after retries end.
- Read [error-handling.md](error-handling.md#recovery-error-types) before branching on the recovery error.

A Flow may register several instances of one Step class under different Step types, such as a generic Step parameterized by input. Give each instance a distinct Step type. Where a decision, failure route, or cancellation can target a class, that target is valid only when exactly one instance of the class is registered; otherwise target the instance, or a Step reference that holds only the Step type.

## State selection and locks

Every WaitFor and Execute call receives ordinary Attributes and Channel size metadata automatically.

- AttributeMap values and pending Channel messages require selections in StepOptions. Select exact instances when the keys are known. Select the whole map only to enumerate it.
- Reading unselected AttributeMap or pending-message state raises a not-loaded error. A selected empty Channel returns an empty list.
- Channel size, and ChannelMap keys and sizes, need no load. AttributeMap size and keys need the whole map loaded.
- WaitFor and Execute load independent snapshots. Execute loads after the Wait consumed its messages.
- WaitFor and Execute have separate lock sets.
- An Attribute lock does not load anything. A load does not lock anything.
- Lock the exact AttributeMap instance for a read-modify-write that can race. For a whole-map invariant, see [attribute.md](attribute.md#locks).

## Context

The Step Context exposes, among others:

- the Flow ID, Run ID, Step execution ID, and attempt number;
- the Flow run's start time. It is the same for every Step, retry, timeout handler, and RPC in one run, while the wall clock differs per retry ([Must 1](../../SKILL.md#must)). Derive identities from the Flow ID, adding this start time when the Flow ID can be reused. It does not identify a run across Flow ID reuse alone.
- the last heartbeat value;
- the recovery error and whether WaitFor failed;
- Timer and SubFlow results of the Wait;
- the attempt deadline and the cancellation signal;
- Attribute, AttributeMap, and Channel access for the loaded snapshot.

## Configure the method policy where defaults do not fit

The defaults fit short, idempotent methods. Usually set StepOptions only for a method that runs long or stays silent, holds a lock, makes a non-idempotent effect, needs a failure route, or loads AttributeMaps or pending messages. The options cover:

- durability override;
- method timeout and, for Execute, the heartbeat timeout;
- retry attempts, backoff, and total duration;
- selective AttributeMap and Channel loads;
- Attribute or map-instance locks;
- the exhausted-retry failure route.

Exact option names and defaults are in the language's Developer reference.

## External effects

- Key each external effect by an ID that is stable across attempts and send it to the target service ([Must 1](../../SKILL.md#must)): the Flow ID plus the Step execution ID when one Step execution is one logical effect (adding the run's start time when the Flow ID can be reused, because a later run numbers executions from the start again), or a business key when a later execution can repeat it.
- Dex retries a possibly-run attempt, so on such a failure reconcile at the target by that key before you repeat or report.
- Record the outcome in the Flow.
- Correctness state lives in Dex, not Worker memory ([Must 2](../../SKILL.md#must)).

Tests for these boundaries are in [testing.md](testing.md#scenarios-to-cover).
