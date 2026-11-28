---
name: dex-app-builder
description: Primary skill for all Dex work with source-available Dex. Use for Dex product, application, or workflow requests; for requests to learn, get started with, set up, or understand Dex; and for standalone SDK work that implements, debugs, tests, or operates code using the Dex SDK in Go, Python, TypeScript, Java, or Rust. Orchestrates business discovery, Flow and FDG modeling, the backend and its application API, and local verification with dexcli dev, plus the user's own UI, Dex Web, or released connectors when the user wants them. Not for official connector-library work.
---

# Dex App Builder

Build reliable Dex applications through Dex's public programming model. Keep
the user's experience in Dex terms and Dex APIs. For product work, build the
smallest coherent Dex product that satisfies the request. A clear
implementation request authorizes its stated UI and routine engineering
choices. Ask only for missing business facts, access, or consequential
choices. Do not turn discovery, design, wireframes, or source checks into
repeated approval gates. A clear build request is done only when every
requested deliverable exists. Never hand back a partial implementation with
the rest only described. Pause only the part that depends on a missing
business fact, access, a required approval, an open consequential decision,
or a required API you cannot verify in the installed package. A capability
missing from a higher-level library, such as an agent package, is not a
missing API: build it with the Dex SDK. Name what is missing and finish the
rest.

The toolchain is source-available Dex: the Dex SDK for the application's
language (Go, Python, TypeScript, Java, or Rust), `dexcli` (including
`dexcli dev` and the FDG analyzer behind `dexcli visualize`), Dex Server
(server-zd), Dex Web, and released official Connectors.

This file is mostly facts: how each Dex mechanism behaves in each case, and
its defaults. Read it fully. A design follows from these facts, four Must
rules, and the patterns that combine several facts for common business shapes.

## Session start and routing

Run the [Dex Skills version check](references/core/plugin-version-check.md)
once per session. Read the lifecycle hook status first. Run its fallback
only when the status is `unavailable` or absent. The check never blocks the
task.

Confirm a writable workspace and file and command tools before you claim
implementation. Without them, provide discovery and a handoff for a
repository-backed host.

Pick the route first:

| Request | Route |
| --- | --- |
| A Dex product, application, or business process, end to end | **Product route**: read [product-route.md](references/product-route.md) before Stage 0, then every section of this file from [Mental model](#mental-model) on |
| Learn, install, set up, or understand Dex, with no concrete process yet | [Getting started](references/getting-started.md) |
| Implement, debug, test, or operate code that uses the Dex SDK, such as fixing a Flow or adding a Step | **SDK-only route**: skip discovery, the UI and Connector questions, and product-route.md. Start at [Mental model](#mental-model) and keep the application's existing language, structure, and versions |
| Add or change an official Connector, operation, Trigger, or UI unit | [Connector Contributor](../dex-connector-contributor/SKILL.md) |

- A request that arrives through the former `dex-sdk` skill takes the
  SDK-only route. Do not expand it into the product workflow.
- An SDK-only task that grows into a new product or process moves to the
  product route. Say so once.
- A missing released Connector capability routes that contribution to
  Connector Contributor. The application itself stays here.
- Honor a skill the user invokes explicitly.
- A missing sibling skill means an incomplete installation. Update the
  three-skill bundle. Do not search arbitrary caches or fall back to
  superseded skills.
- `dex-developer` is a former name of this guidance. If a direct
  `dex-developer` install is also present, tell the user to remove it and
  install the plugin, then start a new session. Do not delete it yourself.

## Mental model

A **Flow** is one durable run of a business process, with a stable Flow ID. It is a graph of **Steps**. Each Step has **WaitFor**, which returns the Conditions to wait on, and **Execute**, which does the work and returns a decision: the next Steps, or how the Flow closes. Each method call is one commit boundary. Dex persists every committed change and retries failed methods, so the Flow survives Worker and Server restarts.

| Concept | What it is | Key point |
| --- | --- | --- |
| [Flow](references/core/flow.md) | durable execution with identity, schema, and RPCs | one Flow per business lifecycle, named after the domain |
| [Step](references/core/step.md) | WaitFor plus Execute; options set durability, timeouts, retries, loads, locks | one recovery boundary; its external effects are idempotent or compensatable |
| [Attribute](references/core/attribute.md) | typed durable state; AttributeMap for keyed instances | the default home of application state |
| [Channel](references/core/channel.md) | durable FIFO queue consumed by a waiting Step | queued intent, not storage |
| [RPC](references/core/rpc.md) | typed synchronous handler on a Flow | the application's write path into a running Flow |
| [Stream](references/core/stream.md) | best-effort, resumable messages | progress only, never the record |
| [Timer](references/core/timer.md) | durable delay Condition | business delays, deadlines, and the interval of a Timer polling loop |
| [SubFlow](references/core/subflow.md) | child Flow started from WaitFor | for a child with its own lifecycle, history, or retries |
| [Client](references/core/client.md) | the application's boundary to Dex | start, invoke, read, wait; no direct state write |
| [Connector](references/core/connector.md) | released provider integration: ready-made Connector Steps (Query or Mutation) and Triggers | released official modules only; long-lived credentials stay in configuration |

The Components are the processes that run this model: [application (Worker and Client)](references/core/application.md), [server](references/core/server.md), [dexcli and local development](references/core/cli.md), [Dex Web](references/core/web.md).

Choose the mechanism:

| Need | Use |
| --- | --- |
| Current state that later Steps, reads, or search need | Attribute or AttributeMap |
| Queued command, approval, or event for the Flow | Channel, published through an RPC |
| Synchronous change or computed read from outside | RPC |
| Read stored values from outside | Client Attribute read |
| Wait for a state change from outside | Attribute-match or Step-completion wait |
| Live progress | Stream, with durable state in Attributes |
| Parallel work in one lifecycle | parallel Steps (GoToMany) |
| Separate owner and lifecycle | another top-level Flow; when another Flow starts it, a Step starts it through a Client |
| Wake the Flow at a time | a Timer Condition, or the Flow timeout for the whole Flow |
| Wait for an external system | a [Polling](references/core/patterns/polling-and-timers.md#polling) shape: WaitFor Timer or long-running Step heartbeat |

## Facts

These facts hold for every Dex application, in every SDK. Defaults are the
current Server and SDK defaults; the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits)
and each SDK's [StepOptions reference](https://docs.superdurable.io/reference/go/step-options#defaults)
hold the current values and the option names. Per-language API names and
signatures are in the SDK reference for the application's language.

### Commits and isolation

- Each WaitFor, Execute, RPC, and timeout handler call is one commit. Its staged Attribute writes, Channel message deletes, and Channel publications commit together with its result, and only when the method returns successfully.
- Hard-delete Attribute values are the exception: they are stored before the commit, so a value written by a failed attempt may be visible.
- A commit is atomic but not isolated. A method reads its snapshot, and Dex does not check at commit time whether what it read is still current. Two methods that read and then write the same value lose an update unless both declare the same Attribute lock.
- External calls are outside the commit, and Dex cannot roll them back. Stream writes and heartbeats are outside the commit too.
- A canceled method loses only what it has not committed. What an earlier method of the same Step committed, such as WaitFor's writes, stays.

### Repeated execution, snapshots, and locks

- Every method may run more than once: on retry, after a Worker crash, after an ASYNC rollback, and as overlapping attempts after a cancellation.
- Dex reserves a Worker slot, then records the attempt start, which fixes the snapshot and takes the declared locks in one step. A method therefore gets its snapshot only after it holds all its locks.
- All retries of one method call reuse the snapshot of its first attempt. A retry does not see writes made after that, so retrying never waits for someone else to fix the state.
- A Step holds its declared locks for the whole method call: through external calls, retry backoff, and time spent queued for a Worker. With no Worker able to run it, it holds them until the Flow times out or is stopped. No command releases one lock. Under the default retry policy (4 hours, unlimited attempts), a failing locked Step can hold its lock for about 4 hours plus its last attempt, longer when retries queue.
- A Step that needs a held lock queues and waits; it does not fail. It runs when all its locks are free and it is first in line for each. A method takes all its locks at once, so locks never form a deadlock cycle.
- An RPC never waits for a lock. When a Step or another RPC holds a lock it needs, the RPC fails at once with a lock conflict. When the lock is free, the RPC goes ahead of queued Steps.
- Dex does not infer locks from what a handler reads or writes. A read or write without a declared lock bypasses the lock. A Flow can declare that writes require the lock, so the Worker rejects an unlocked write.
- WaitFor and Execute each have their own snapshot and lock set; they never share one commit.

### RPCs

| RPC kind | Behavior |
| --- | --- |
| Read-only: no lock, not transactional, returns only an output | Changes nothing. It also succeeds on a closed run the Server still retains, so its success does not prove the Flow is running. |
| Transactional, including every RPC that holds a lock | Its reads and writes commit as one atomic operation, and Dex checks at commit that each message it deletes is still pending. |
| Any RPC that locks, is transactional, or writes, publishes, or moves Steps | A new invocation needs an active run. A closed run rejects its commit, even after the handler ran. A recorded request ID of a transactional or locking RPC still replays its reply after the run closes, while the run is retained. |

- Each call carries a request ID, and the SDK generates a new one per call by default. Dex compares request IDs, not inputs.

| RPC with an already recorded request ID | Behavior |
| --- | --- |
| Transactional or locking | Returns the recorded reply without running the Worker; concurrent calls with the same ID share one execution. |
| Other, returning durable effects (Flow changes or recorded events) | Runs the handler again, repeating its external calls; the commit returns the recorded reply and applies no second effects. |
| Other, returning no durable effects (no Flow changes and no recorded events) | Runs the handler again and returns its new output; nothing is recorded. |

- A handler that fails without committing records nothing, so a retry runs it again. A caller-side deadline, a cancellation, or UNAVAILABLE after the Server's commit budget leaves the outcome unknown: the RPC may still commit. On a run over its state budget, an RPC that changes no Flow state keeps no reply or request ID.
- Each run keeps only its newest 1000 request IDs by default; an older ID runs as a new request. A new run starts with none.
- One RPC handler call is limited by the RPC timeout, 60 seconds by default, which the Client cannot override. Long work belongs in a Step.
- An RPC fails when no Worker takes it within the dispatch wait; a Step attempt waits for a Worker instead.
- An RPC can cancel Steps by Step type across the Flow. Dex picks the Steps to cancel when the RPC commits, not from the handler's snapshot, so a selected Step may already have committed its result. The handler cannot know the outcome; a later Step reads committed state.
- A successful RPC that publishes confirms only that the message is stored, not that a Step has handled it.

### WaitFor and Execute

- WaitFor returns the Conditions to wait on. Its result can also write Attributes, publish and delete Channel messages, and write Step execution local values, which pass a value from WaitFor to Execute within one Step execution.
- WaitFor has its own retry policy. When its retries run out, its failure policy applies: by default the Flow fails; it can instead enter Execute with the recovery error. A retried WaitFor reuses its original Dex state snapshot; an external read made again can return a different provider value. Only the committed WaitFor result defines the Wait.
- Execute runs when the Wait is satisfied, reads which Condition fired from its Context, and returns the decision. When its retries run out, its failure route runs if the Step has one; otherwise the Flow fails.
- A SubFlow Condition starts its child Flow from WaitFor.
- A failure route can tell the work certainly did not run only when the recovery error reports that no attempt started, which SYNC durability can report; ASYNC failures always report that the work may have run. A failure that may have run may still have taken effect at the provider.

### Conditions and Channel messages

- Dex checks a Wait when it starts, then again after each committed change that can satisfy it: a Channel change, a fired Timer, an ended SubFlow, or a graceful completion request. Messages queued before the Wait started satisfy it at once; otherwise the Condition whose event commits first wins.
- When several Conditions are ready in the same check, AnyOf selects Timer, then Channel, then SubFlow, then graceful completion; within one kind, declaration order. Across kinds the argument order has no effect. Two events rarely become ready in the same check, so the first committed event usually decides.
- Only the selected Condition consumes messages: in AnyOf, the selected Channel Condition; in AnyCombinationOf, the selected combination. Other Conditions' messages stay queued.
- When the Timer wins, no message is consumed. A message published after that is still accepted, and the RPC that published it returns success.
- Execute reads which Condition fired from its Context: whether the Timer fired, and the messages each Channel Condition consumed.
- When a Step decision or an RPC cancels a Step before its Execute commits, the messages it consumed return to the front of their Channel in their original order. The canceled Execute may already have seen them, so a message can be handled twice.
- When the Flow close itself cancels the Step, the consumed messages are dropped instead: StopFlow, the Flow timeout, a failure that closes the Flow, or a decision that force-completes or force-fails it. A graceful completion returns them, and they stay in the closed run.
- Several Steps may wait on one Channel. Dex offers a new message to the waiters in the order their waits started. A wait that needs more messages than are queued is skipped, so a wait for n messages can wait forever while one-message waits take each message. Waiters that take one message each and may take any message, such as a worker pool, can share a Channel; which one receives a message depends on timing. For an event every waiter must receive, use one ChannelMap key per waiter.
- A graceful completion Condition wakes only the Waits that include it, and a Wait has at most one. It ranks last in AnyOf, so a ready message wins over it.

### Timers

- A Timer's deadline is the Server timestamp of the WaitFor result that declared it, plus its duration. The Timer is durable once that result commits.
- A Timer never fires before its deadline unless SkipTimer skips it. It can fire late when the Server is busy or fails over; a periodic full scan, every hour by default, catches a Timer whose wake-up was missed. When acting late is wrong, such as a reminder after the event, check the clock against the stored deadline.
- Only Timer Conditions and the Flow timeout wake a Flow at a time. A local sleep or an in-process scheduler does not survive a Worker restart.
- SkipTimer makes a waiting Timer ready at once through the Client, so a test can keep the business duration. The Flow timeout is a Timer too and can be skipped the same way.
- Workers of one Flow type on one task queue share its tasks, so a test Worker and another Worker on the same Server and queue take each other's work. Draining a Worker waits for its running handlers up to the drain timeout, then requests cancellation, while a crash fails the delivered attempts at once; the two show different recovery.

### Flow timeout, completion, and cancellation

| Flow timeout policy | When the timeout fires |
| --- | --- |
| Fail | The Flow closes as failed. |
| Cancel | The Flow closes as canceled, with no retry. |
| Handler | The timeout handler runs; the Flow closes by its decision. Handler requires a registered timeout handler; the SDK rejects this policy when the Flow has none. |

- A Flow timeout of 0 means none. With a positive timeout, the SDK defaults to Fail, or to Handler when the Flow has a timeout handler.
- The Flow timeout is a start option. Start options cannot change after the Flow starts; a limit that can move needs a Timer, such as the [deadline watchdog](references/core/patterns/polling-and-timers.md#deadline-watchdog).
- The Flow timeout is a Timer, so it can fire late, and the close cancels running handlers cooperatively. When a Fail or Cancel timeout is processed, Dex begins closing and stops scheduling new Steps. Under Handler, other Steps can keep running and start successors until the handler commits a closing decision. Neither policy forcibly stops external calls already under way.
- Without a Flow timeout, a Step that no Worker takes waits forever, and so does a SubFlow without its own timeout.
- The timeout handler has Execute semantics: its own method timeout, retries, heartbeat, locks, loads, and failure route. It runs once per run; there is no Flow-level retry. Active Steps keep running while it runs. When it commits ForceComplete or ForceFail, Dex starts closing the Flow and cancels the other Steps. GracefulComplete from it waits for every other active Step to end.
- A Flow start rejects a positive start delay and a Flow retry policy.
- GracefulComplete ends the deciding branch and requests completion. Completion starts when no Step is active; a later DeadEnd does not withdraw the request, and no new Step starts after completion begins.
- Dex never closes a Flow because no Step is active. Without a pending graceful completion, the Flow runs until its timeout, a stop, or a decision that closes it.
- ForceComplete and ForceFail begin closing the Flow and cancel the active Steps when the decision commits; consumed messages are dropped. A natural close, such as these or a graceful completion, records the terminal status only after admitted transactional or locking RPCs finish and pending Attribute Store writes are written or dropped. Until then the run reads as RUNNING and WaitForFlow keeps waiting. StopFlow cancel, fail, or terminate does not wait for these barriers.
- ForceCompleteIfChannelsEmpty checks the named Channels atomically when the decision commits. A Channel is non-empty while it has pending messages, including messages a canceled Step returned, or a message consumed by a Step whose Execute has not finished. If all are empty, the Flow begins completing; otherwise the decision starts the given fallback Steps.
- Cancellation is cooperative. The SDK signals the handler, and the handler must observe it; a handler that ignores it keeps running and its external calls still happen. Dex discards a canceled Step's later result, retries, and failure route. A decision or RPC never cancels the Step it starts.
- StopFlow (cancel, fail, or terminate) closes the run when the stop commits and runs no more application code: no decision, failure route, or cleanup.

### Starting a Flow and ID reuse

- StartFlow returns once the Server accepts it, and writes the initial Attributes atomically with the Flow's creation, so the first Step sees them.
- The Flow ID is the business identity. A Flow ID with no run always starts.
- The ID reuse policy decides by the **latest run** only: whether it is running and how it closed, never what it saved. A run that recorded a business failure and then completed is COMPLETED.

| ID reuse policy | Latest run RUNNING | Latest run COMPLETED | Latest run FAILED, CANCELED, or TERMINATED |
| --- | --- | --- | --- |
| Disallow reuse (the default, also when unset) | reject | reject | reject |
| Allow if previous failed | reject | reject | start a new run |
| Allow if no running | reject | start a new run | start a new run |
| Terminate if running | terminate it, then start a new run; never rejects | start a new run | start a new run |

- A Flow timeout under the Fail or Cancel policy closes the run as FAILED or CANCELED; under Handler, the handler's decision decides.
- Every start carries a Request ID; the SDK fills a random one when the caller omits it. Dex compares the Request ID with the one that started the **latest** run, and compares nothing else: not the input, not the options.

| Request ID compared with the latest run's | ignore-already-started off | ignore-already-started on |
| --- | --- | --- |
| Same: a retry of that start; the reuse policy is not applied | already-started error, under every policy | returns that run's Run ID, under every policy |
| Different, or omitted | the reuse policy decides; a rejection is an already-started error | the same: ignore-already-started applies only to the same Request ID |

- So a retry with a changed input is told apart only when the Request ID derives from the complete logical start request. After a newer run starts under the same Flow ID, a late retry of an older start counts as a new request and goes to the reuse policy.
- Start deduplication lasts only while a run of the Flow ID exists. A closed run is retained for 7 days by default, then purged; after that the Flow ID starts again even under Disallow reuse, and the new run repeats its external effects unless the receiver drops the repeated key. Deduplication that must outlive retention lives in the application's own record or at the receiver.
- Terminate if running is the only policy whose start closes a running run. A retry with the same Request ID never terminates the run it started.

### Runs, reads, and outputs

- The Server creates the Run ID. Time travel keeps the Run ID and increments the run attempt; an idempotency key that must change after time travel combines both.
- The Context carries the Flow start time, the same for every Step, retry, and RPC in one run, and the first-attempt time of the current method. The current time differs on every retry.
- A Client Attribute read is answered by the Server without a Worker, from one committed state. Client Attribute reads, DescribeFlow, and a StopFlow without a Run ID target the current or latest run. A retained closed run stays readable until it is purged. An Attribute-match wait targets only the active run.
- A Client Attribute read or read-only RPC started after a successful write sees the committed state of the same run, including that write unless a later write replaced it. This does not cover a reused Flow ID, a failed or unknown write outcome, the Steps the write triggers, or projections.
- Only a COMPLETED Flow keeps the output of its completing decision. A failed, canceled, or terminated Flow has no output, even after an earlier GracefulComplete output; so does a timed-out Flow under the Fail or Cancel policy. A timeout handler that completes the Flow keeps its completion output.
- Step outputs live only in history. A result or status a user must see needs a read path: a result Attribute, a read-only RPC, or the Flow's completion output.

### SubFlows and child Flows

- The Server derives a SubFlow ID from the parent Flow ID, the starting Step type, a counter, and the Condition index. A Step type that starts a SubFlow must not contain `-`; Dex fails that WaitFor attempt without retry. A child that people look up, retry, reopen, or stop by its own number is therefore a top-level Flow whose business number is its Flow ID.
- Dex first compares the Request ID it generated for the SubFlow start. When it matches, Dex attaches to the running child or returns its final result, under every policy. Otherwise the SubFlow reuse policy decides:

| Existing child | Attach | Restart if previous exits abnormally (default) | Always restart |
| --- | --- | --- | --- |
| None | start | start | start |
| Running | attach | attach | terminate it, then start |
| COMPLETED | return its result | return its result | start |
| FAILED, CANCELED, timed out, or TERMINATED | return its result | start | start |

- A later run of the same parent Flow ID counts Step executions from the start again, so it derives the same SubFlow IDs. Under the default it reuses the earlier running or completed children; Always restart terminates a running one. Different Step executions or Condition indexes within one parent derive different child IDs; only when a later parent run derives the same ID and both versions of that child must keep running does the later run need a different parent Flow ID.
- A child keeps running when its parent Step is canceled or its parent Flow closes; Dex does not stop it. This holds for SubFlows and for top-level children.
- The serialized outputs a child delivers to its parent total at most 16 MiB by default, blob-backed outputs counted at their full size. Exceeding it fails the child's completing attempt without retry. For larger data, store it outside the result and return a small application-owned reference.
- A delivered child result makes the parent's SubFlow Condition ready; it does not prove the parent consumed it or completed later work.
- A child can end without reporting: StopFlow runs no more of its code, a failure after a Step exhausts its retries runs only its failure route, and its Flow timeout closes it.

### Joins

- A Join is a named ChannelMap that counts committed branch result messages per round; it does not infer one completion per branch. The branch's designated reporting Step, its recovery Step, or an RPC reports once, with its method's commit, so a retried report counts once. One round's results never satisfy another round's join.
- Canceling a branch produces no result and runs no failure route; a report committed before the cancel stays. Dex picks the Steps to cancel at commit time, so a canceller cannot safely report for a branch from its own snapshot.
- A decision that cancels sibling Steps selects the Steps one decision started together; the later Steps of a multi-Step branch are not those siblings. A partial join consumes only the results it selected. Uncanceled late branches keep reporting, and their results stay pending until a reset consumes them or the Flow closes. A round whose branches were canceled cannot be reset, because they never report.
- Waiting for all branches of an empty list is satisfied at once; waiting for the first or n of an empty list fails.

### Attributes, loading, and secrets

- Ordinary Attributes and Channel sizes load automatically. AttributeMap values and pending messages load only when the StepOptions select them; reading one that is not loaded fails. A lock loads nothing.
- A hard-delete Attribute lives only in object storage, and history keeps only a reference. Each Flow ID keeps one stored value per key, overwritten in place and shared by later runs of that Flow ID. Even a start rejected because the Flow ID is running overwrites the initial value. After delete or null, the value is gone for good; time travel does not restore it.
- Ordinary writes can stay in retained history after an overwrite or a delete, subject to the run's history window and value retention. A delete is not immediate erasure, and history is not a permanent archive.
- So long-lived credentials stay outside Flow state: the application reads them from its configuration or environment at run time, and with a released Connector through its language's Connector SDK project configuration loader. A short-lived secret the Flow itself must hold goes in a hard-delete Attribute and is deleted after use.
- Payload encryption leaves indexed and plaintext Attributes, names, and IDs as plaintext on the Server; they are what search and Attribute matching compare.
- Search results lag committed writes: a miss does not prove a committed value is absent. A Flow decision reads its own state, not search.
- Attribute Store sync projects the latest Attribute values into the application's PostgreSQL tables in the background. Commits never wait for it; a write can repeat, entries still pending after its retry budget are dropped without affecting the Flow, and it does not backfill old values or a new store. Treat the table as eventually consistent.
- Index keys share the Server's finite search capacity, some backends cap slots per type, and the slot registry has no delete. Renaming an index key does not backfill existing Flows.

### Streams

- A Stream write is sent at once, outside the commit, and a later failure does not roll it back. Each write counts as a heartbeat. When the Stream Store is unavailable or full, the message is dropped and the Step does not fail.
- Streams live in Server memory by default and are lost on restart or failover. Capacity is shared per Flow type and Stream name, and old messages are trimmed.

### Workers, attempts, and time bounds

- Dex routes a task by task queue and Flow type, not by Step type. Within one task queue a Flow type name maps to one Flow definition, and Flow types must be unique within one registry. Sticky routing is best effort: after a short wait, or when the original Worker disconnects, any Worker can take the task. A later Step or RPC may run on another Worker, so state a later handler needs lives in Dex, not in Worker memory or on one Worker's disk.
- One attempt number goes to at most one Worker. If the Worker crashes or disconnects after delivery, the attempt fails and uses a retry. Only when Dex knows no Worker ran it, such as an unknown Step type, an undelivered task, or a task returned without running during a clean Worker shutdown, does the attempt return to pending without using a retry. A drain lets running handlers finish and commit; an attempt interrupted after it may have run, including at the drain deadline, fails and uses a retry. Under ASYNC durability a Server node crash can run the same attempt number again.
- A Step that no Worker takes waits in the durable task queue. It does not fail and uses no retry; only the Flow timeout or a stop ends that wait.
- The method timeout and the heartbeat timeout start when the task is sent; waiting for locks or in the queue does not count.
- The heartbeat timeout is 1 minute by default. An attempt that sends no heartbeat and writes no Stream for that long times out, and raising only the method timeout does not move that deadline. A call that can stay silent longer needs a higher heartbeat timeout or heartbeats from a background task.
- Step method defaults: method timeout 2 hours per attempt; retry backoff 1 second, doubling up to 100 seconds; retry total duration 4 hours, with unlimited attempts within it; durability SYNC.
- The retry total duration counts from the first delivery. Dex checks it only after an attempt fails, before scheduling another retry, and never stops a running attempt. An already scheduled retry may wait in the queue past the budget and still run for a full method timeout. With Workers promptly available the overrun is about one method timeout; queueing can make it longer. Retrying can also stop up to one backoff interval early.
- When work must happen at a deadline regardless of a retrying method, use the [deadline watchdog](references/core/patterns/polling-and-timers.md#deadline-watchdog): it requests the deadline action independently. Timers can fire late and cancellation cannot undo or force-stop an external call, so a strict external cutoff needs a deadline check or fence at the receiver.
- SYNC durability, the default, stores the attempt start before the Worker receives the task and stores the method result before downstream work starts. ASYNC may start the attempt and downstream work before those writes are stored; a Server node crash can lose the result and rerun the method and the work started from it. Either way, external effects must be safe to repeat.
- Concurrent executions share the registered Flow and Step objects, so per-invocation state belongs in the Context and the input, not in mutable fields of those objects.
- A returned error is retried until the retry budget runs out, even when the provider confirmed it is permanent.

### Default limits

The Server rejects a request that would exceed a limit. In this table a "direct write" or "direct publish" is a FlowService request outside a Step or RPC, such as one from Dex Web or dexcli; the SDK Client has no such methods. The values are current defaults; the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits) has the option names.

| Limit | Default | When exceeded | Workarounds |
| --- | --- | --- | --- |
| Active state of one Flow run | 64 MiB | A start, direct write, or RPC that grows active state over the budget fails with RESOURCE_EXHAUSTED; retrying without shrinking the request or freeing state fails again. Deletes, consumption, and requests that do not grow state still run. A Step's heartbeat or completion that grows it fails the attempt without retry. | Keep growing collections in a [sequentially chunked AttributeMap](references/core/patterns/flow-shapes-and-state.md#sequentially-chunked-attributemap) and delete or archive old chunks. Bound a queue with [back pressure](references/core/patterns/parallel.md#back-pressure). Data that cannot fit stays in an external store, and the Flow keeps references. |
| One value, such as an Attribute value or a Channel message | 16 MiB | A start, direct write, or RPC with a larger value fails with INVALID_ARGUMENT, and so does every retry of the same value. A Step heartbeat or completion with a larger value fails the attempt without retry; its failure policy then applies. | Split or chunk the value, or keep it in an external store and keep a reference. |
| Pending messages in one Channel | 1000 | A publication that would exceed it fails as a whole: a direct publish or RPC with RESOURCE_EXHAUSTED, a Step completion by failing the attempt without retry. Messages that a canceled Step returns never fail. | Split a longer waiting list across Channels. |
| Active Step executions in one Flow run, waiting ones included | 1000 | A start, RPC, or Step completion that raises the count above it fails the same way. A Step that completes and starts one next Step keeps the count. | Batch the work. Run a very large fan-out as SubFlows or top-level Flows. |
| Request IDs kept per run | 1000 | An older request ID runs as a new request. | A long-lived Flow that must reject a late repeat checks a business key in its own state. |
| Concurrent waits on one Flow, such as Attribute-match waits | 1000 | A wait beyond it fails with RESOURCE_EXHAUSTED. | Let one reader wait and share the result, or read on demand. |
| Results a child Flow hands to its parent | 16 MiB in total | The child's Step completion that would exceed it fails the attempt without retry; unless its failure policy recovers, the child fails and the parent receives that failure. | Store the data outside the result and return a small application-owned reference. |

## Must

These are the only hard rules. Each holds in every business scenario, and
breaking it always breaks correctness under the facts above. Everything else
in this skill is a fact, a pattern, or a suggestion.

1. **Every external effect is safe to repeat.** Key each provider write from WaitFor, Execute, an RPC, or a timeout handler by an ID that is stable across attempts, such as a business key, or the Flow ID with the Step execution ID, plus the Flow start time from the Context when the Flow ID can have later runs, and send it to the receiver; or make the effect compensatable. Never derive that ID from the current time, a random value, or anything else that differs per attempt. Facts: [external calls are outside the commit](#commits-and-isolation), [every method may run more than once](#repeated-execution-snapshots-and-locks), and [a crash after delivery fails the attempt as possibly run](#workers-attempts-and-time-bounds).
2. **Correctness lives in Dex, not in a Worker.** Keep state, pending work, and times that correctness depends on in Attributes, Channels, Timers, or the Flow timeout, never only in Worker memory, a local sleep, or an in-process scheduler. Facts: [a later handler may run on another Worker](#workers-attempts-and-time-bounds) and [only Timers and the Flow timeout wake a Flow](#timers).
3. **Racing read-modify-writes share one lock or one writer.** When two methods can run concurrently and both read then write the same value, both declare the same Attribute lock, or the writes go through a Channel to one consuming Step. Facts: [commits are not isolated](#commits-and-isolation) and [undeclared reads and writes bypass locks](#repeated-execution-snapshots-and-locks).
4. **Open runs keep their contract.** While open runs must finish on new code, keep their Step types, RPC names, and stored data compatible; otherwise an open run meets missing Step implementations, renamed RPCs, or incompatible data. See [application.md](references/core/application.md#versioning-flow-code).

## Patterns

A pattern combines several facts for a common business shape: when the
business needs X, a shape that works is Y, because of the facts it names.
Patterns are suggestions; derive a variant from the same facts when the
business differs. A consequence of one or two facts is not a pattern: read
the facts.

Before you design a Flow, find its shape below and read the matching section.

| Business shape | Pattern | Shape | Derived from |
| --- | --- | --- | --- |
| Wait for a person's or system's decision, with a deadline or escalation | [Decision with Deadline](references/core/patterns/control-structures.md#decision-with-deadline) | One waiting Step races the decision Channels and a Timer with AnyOf, and Execute routes on the Condition that fired, never on a missing message. After the deciding Execute commits, a later event is answered as a new event. | [Conditions](#conditions-and-channel-messages), [Timers](#timers), [Commits](#commits-and-isolation), [Locks](#repeated-execution-snapshots-and-locks), [RPCs](#rpcs) |
| A caller must learn whether its reply made the deadline | [Close a reply window](references/core/patterns/control-structures.md#close-a-reply-window-with-an-attribute-lock) | Every deciding RPC and the deciding Execute share one Attribute lock and check and close the same window. For a hard cutoff, the reply RPC also compares the clock with the stored deadline. | [Locks](#repeated-execution-snapshots-and-locks), [RPCs](#rpcs), [Conditions](#conditions-and-channel-messages), [Timers](#timers) |
| One event outranks another, such as a withdrawal over a decision | [Arbitrate by business priority](references/core/patterns/control-structures.md#arbitrate-by-business-priority) | The design keeps the priority rule and states whether "the same moment" means commit order or events admitted before the decision. | [Conditions](#conditions-and-channel-messages), [Locks](#repeated-execution-snapshots-and-locks) |
| Take replies until a resolution arrives or the conversation goes idle | [Idle Timer with Resolution](references/core/patterns/polling-and-timers.md#idle-timer-with-resolution) | One Step waits on replies, the resolution, and an idle Timer, records the replies, and loops back for a fresh Timer. | [Conditions](#conditions-and-channel-messages), [Timers](#timers) |
| Bursts of events per entity, each entity handled in order by one short Flow | [Drain an external Channel](references/core/patterns/channels-and-updates.md#drain-an-external-channel) | One Flow ID per entity under Allow if no running: RPCs publish to a Channel, one Step handles each message and closes with ForceCompleteIfChannelsEmpty, and the caller starts a new run when none is active. | [Flow completion](#flow-timeout-completion-and-cancellation), [ID reuse](#starting-a-flow-and-id-reuse), [RPCs](#rpcs) |
| Close or act after a period of inactivity | [Idle Timer](references/core/patterns/polling-and-timers.md#idle-timer) | AnyOf(Timer(window), activity Channel); activity loops back for a fresh Timer, and a fired Timer acts. | [Conditions](#conditions-and-channel-messages), [Timers](#timers) |
| A collection that can grow without a small business bound | [Sequentially chunked AttributeMap](references/core/patterns/flow-shapes-and-state.md#sequentially-chunked-attributemap) | Appends load only the current chunk, reads load one page, and a long-lived Flow deletes or archives old chunks. | [Default limits](#default-limits), [Loading](#attributes-loading-and-secrets), [Locks](#repeated-execution-snapshots-and-locks) |
| Keyed records with direct lookup | [Hash-partitioned AttributeMap](references/core/patterns/flow-shapes-and-state.md#hash-partitioned-attributemap) | A stable hash of the key picks one of a fixed number of map instances, and only that instance is locked and loaded. | [Default limits](#default-limits), [Loading](#attributes-loading-and-secrets) |
| Wait for an external system to become ready or finish | [Polling](references/core/patterns/polling-and-timers.md#polling) | A bounded WaitFor Timer loop with one provider call per Execute, or one long-running Execute with short calls and heartbeats. | [WaitFor and Execute](#waitfor-and-execute), [Workers and time bounds](#workers-attempts-and-time-bounds), [Snapshots](#repeated-execution-snapshots-and-locks), [Streams](#streams) |
| Work must happen at a deadline that spans retrying work, such as a notice or a withdrawal | [Deadline watchdog](references/core/patterns/polling-and-timers.md#deadline-watchdog) | The work runs as the only branch of a Join, and a join Step waits on its result or the deadline Timer, then decides alone. | [Timers](#timers), [Time bounds](#workers-attempts-and-time-bounds), [Cancellation](#flow-timeout-completion-and-cancellation), [Joins](#joins) |
| Recurring work, or reminders until the recipient opts out | [Cron schedule](references/core/patterns/polling-and-timers.md#cron-schedule), [Reminder](references/core/patterns/polling-and-timers.md#reminder) | Cron starts the run and the next Timer wait together; Reminder races the Timer with the opt-out, then rearms after sending. | [Timers](#timers), [Conditions](#conditions-and-channel-messages) |
| A failure that a person must resolve | [Manual recovery](references/core/patterns/failure-handling.md#manual-recovery) | The Execute failure route goes to a Step that waits on retry and skip Channels, and Execute routes on the one that fired. When a losing decision must not stay queued for the next failure, or a caller must learn whether its decision counted, the RPCs check and claim the failure under one lock. | [WaitFor and Execute](#waitfor-and-execute), [Conditions](#conditions-and-channel-messages), [Locks](#repeated-execution-snapshots-and-locks) |
| Undo earlier Steps after a Step fails | [Execute failure recovery](references/core/patterns/failure-handling.md#execute-failure-recovery) | The failure route compensates. When the failed Step made an external call, it undoes that Step first with its original reference, and every undo is a no-op for an action that never happened. | [WaitFor and Execute](#waitfor-and-execute), [Commits](#commits-and-isolation), [Workers](#workers-attempts-and-time-bounds) |
| Keep trying an unreliable call for up to a time, such as an hour | [Time bounds](references/core/step.md#time-bounds) | The retry total duration plus an Execute failure route. The last attempt can run past the limit, by about one method timeout when Workers are available and longer when retries queue. When work must happen at the deadline, add the deadline watchdog; a strict external cutoff also needs a check at the receiver. | [Time bounds](#workers-attempts-and-time-bounds), [WaitFor and Execute](#waitfor-and-execute) |
| A fixed deadline for the whole process | [Graceful timeout](references/core/patterns/failure-handling.md#graceful-timeout) | The Flow timeout and its timeout handler, not a competing Step. | [Flow timeout](#flow-timeout-completion-and-cancellation) |
| Independent branches of one lifecycle, then a join | [Parallel Steps](references/core/patterns/parallel.md#parallel-steps) | GoToMany; a Join waits for all, the first, or n branches. Branches that need their own retries, failure routes, or durable results are Steps, not tasks inside one Execute. | [Joins](#joins), [Cancellation](#flow-timeout-completion-and-cancellation), [Snapshots](#repeated-execution-snapshots-and-locks) |
| Branches take turns on shared multi-Step work | [Critical section](references/core/patterns/control-structures.md#critical-section) | A handoff Step takes the only token, and every exit that leaves the Flow running returns it, including each Step's Execute failure route and early exits. | [Conditions](#conditions-and-channel-messages), [Cancellation](#flow-timeout-completion-and-cancellation), [Commits](#commits-and-isolation) |
| A multi-step mutation started from an API | [Own downstream starts durably](references/core/flow.md#own-downstream-starts-durably) | The API starts one domain-named Flow, and its Steps start the rest; the API handler never chains dependent starts. | [Starting a Flow](#starting-a-flow-and-id-reuse), Must 2 |
| Child jobs tracked by their own number: looked up, retried, stopped, or started by others | [Top-level child Flows](references/core/flow.md#child-jobs-with-their-own-identity) | A Step starts each child through the Client with a stable Flow ID and Request ID; the child reports through an RPC or the parent reconciles its terminal state. When anything else can start the child's Flow ID, the parent claims an already-started run only when its stored business identity matches. | [ID reuse](#starting-a-flow-and-id-reuse), [Runs](#runs-reads-and-outputs), [SubFlows and child Flows](#subflows-and-child-flows), [Flow timeout](#flow-timeout-completion-and-cancellation) |
| At most N jobs run at once per owner, with a bounded waiting list and a close that drains | [Long-lived parent](references/core/patterns/parallel.md#long-lived-parent), [Back pressure](references/core/patterns/parallel.md#back-pressure) | One long-lived Flow per owner runs N worker Steps that each take one request from a shared Channel, and admission, close, and each take share one lock on the close flag. | [Conditions](#conditions-and-channel-messages), [Default limits](#default-limits), [RPCs](#rpcs), [Locks](#repeated-execution-snapshots-and-locks) |
| Pause, stop, or resize running work through RPCs | [Control Long-Running Work](references/core/patterns/channels-and-updates.md#control-long-running-work) | RPCs write control Attributes that each iteration reads. | [Starting a Flow](#starting-a-flow-and-id-reuse), [Snapshots](#repeated-execution-snapshots-and-locks) |

The [pattern index](references/core/patterns.md) lists every pattern, including
Flow boundaries, entity Flows, Workflow DSL, and durable AI agents.

### Suggestions

Usual choices that a design may leave when the business needs otherwise.

- **Start first.** Call StartFlow first with a stable Flow ID and a Request ID derived from the complete logical start request; a read before or after a successful start adds nothing, because the start itself is atomic per Flow ID. Pick the reuse policy from the [ID reuse table](#starting-a-flow-and-id-reuse) by which earlier runs the business may start again under the same Flow ID. See [error-handling.md](references/core/error-handling.md#start-first-reconcile-only-after-an-error).
- **WaitFor sets up the wait.** Make provider writes in Execute, not WaitFor. WaitFor has no failure route or compensation point of its own, and Dex Web shows WaitFor as the wait, so a write there is hard to see and to recover.
- **Poll from Execute.** Usually poll with a WaitFor Timer and one provider call per Execute, or one long-running Execute with heartbeats; a read in Execute gets Execute's retries and failure route, and its result commits.
- **Race a Timer in one waiting Step.** Usually race a Timer with a Channel in one waiting Step, so Execute sees which fired, instead of a Timer-only Step that an RPC cancels.
- **Names.** Prefer domain names for Flow and Step types, such as `ReserveStock`, over `Init`, `Start`, or `Process`, and verb phrases for RPC names, such as `getQueuedMessages`. Prefer one Step type per kind of work, so the graph shows it and it can have its own options.
- **Commit boundaries.** Give a provider action its own Step when its success deserves its own checkpoint, retry policy, failure route, or audit boundary.
- **The Flow owns decisions and status.** Pass business facts as start input or initial Attributes and let Steps compute decisions and status, so the rules live in one place and status changes commit with the Flow's progress. See [client.md](references/core/client.md#the-flow-owns-decisions-and-status).
- **Flow boundaries.** Each call between Flows adds network and coordination cost and repeats when the calling method retries. A mutating call adds durable work and history to the target; a read-only call need not. Usually keep work whose parts would call each other on every operation, callback, or poll in one Flow; split anyway when the parts have different owners or lifecycles and the per-call cost is acceptable. See [flow.md](references/core/flow.md).
- **Parallel Steps before SubFlows.** Prefer parallel Steps inside one Flow, or a separate top-level Flow for a separate owner. Use a SubFlow when the child needs its own lifecycle, history, or retries, or when one fan-out is very large. See [subflow.md](references/core/subflow.md#when-to-use-a-subflow).
- **Dex-first state.** Keep new process state in Attributes and AttributeMaps. Use a store outside Dex when another system already owns the fact, or Dex's model does not fit an access pattern or scale need; name one owner per fact. See [data-handling.md](references/core/data-handling.md#dex-first-state-ownership).
- **Route confirmed permanent errors.** When a provider confirms a permanent error, route it from Execute to a failure-handling Step or ForceFail instead of returning it. A permanent WaitFor read error is routed the same way; see [step.md](references/core/step.md#waitfor-versus-execute).
- **"Not ready" is a normal loop.** In a polling loop, use a normal GoTo for "not ready yet" instead of a retry; retries mix not-ready with real failures and spend the retry budget.
- **Cancellation reaches the call.** Pass the cancellation signal into each call that accepts one, and give a call that cannot observe it its own client timeout, such as a blocking call in a Rust handler or a Go call without the context; otherwise it keeps its Worker slot until it returns.
- **Provider-supplied deadlines.** Read them with a read-only call in WaitFor and return the Timer. Read them instead in the previous Step's Execute and pass them as input when the value must be auditable or visible, or needs its own failure route.
## Find the right reference version

This file states the design-relevant facts and defaults. Exact API names, signatures, option names, error classes, configuration keys, and version differences live in the SDK doc comments and the versioned Developer reference.

1. Inspect the existing manifests and source before you select tools. Find the installed SDK version in the project manifest or lockfile: `go.mod` (Go), `pyproject.toml` or the lockfile (Python), `pom.xml` or `build.gradle` (Java), `package.json` or the lockfile (TypeScript), `Cargo.toml` or `Cargo.lock` (Rust). Note its minor version.
2. Open the Developer reference for that component. `https://docs.superdurable.io/reference/<component>/` is the latest version; components are `go`, `python`, `java`, `typescript`, `rust`, `server`, `cli`, and `web`.
3. Select the project's version with the version selector in the left sidebar, then use the URL it opens. The [reference overview](https://docs.superdurable.io/reference/) has the compatibility matrix.
4. For the Server, the CLI, and Dex Web, use the version that is deployed or installed (`dexcli version`).
5. To read a docs page as text, follow its **View as Markdown** link when available, then select the Markdown variant for the application's language if the page lists one.

- An existing project keeps its pinned language toolchain, Dex SDK, `dexcli`, Server, Connector, and frontend versions unless the user asks for an upgrade.
- Treat the project's source, lockfile, installed SDK doc comments, and the matching runnable [examples](https://github.com/superdurable/dex/tree/main/examples/) as authoritative for exact code. Copy API shapes from runnable examples, not from memory. Examples track Dex `main` at the time this skills release was built; check them against the installed version.
- If you cannot verify an exact API for the installed version, stop at the version-independent Flow model and ask to inspect the installed package. Never invent a Dex API.

## Stay at the application boundary

- Model behavior with Flows, Steps, Waits, Attributes, Channels, Streams, RPCs, Timers, SubFlows, Connectors, Workers, and the Client.
- The SDK Client has no direct state write; outside code changes a running Flow through typed RPCs. Inside a Step or RPC, usually read and update its own Flow through the handler Context: a Client call into its own Flow is not part of the handler's commit, reads the latest committed state instead of the handler's snapshot, and can fail on the handler's own locks. From Execute, a Client usually serves another Flow, such as a top-level Flow or a SubFlow stopped by the ID its Context supplies. Read stored Attributes with the Client Attribute read, and use a read-only RPC when the read needs Flow code, pending messages, or a whole AttributeMap. Wait for a state change with an Attribute-match wait.
- Inspect and recover with Dex Web, dexcli, SDK errors, and application logs.
- Do not present Dex Server internals as application requirements. Discuss them only when the user develops or operates Dex itself.
- Diagnose read-only. Do not stop, time travel, publish, invoke, delete, edit, or otherwise mutate a Flow unless the user authorizes it.

## Load references

1. Read the language entry file for the project: [Go](references/go/go.md), [Python](references/python/python.md), [Java](references/java/java.md), [TypeScript](references/typescript/typescript.md), or [Rust](references/rust/rust.md). Load only that language.
2. Before you write any Client boundary (start, RPC, wait, cleanup, admission, Stream write), read [error handling](references/core/error-handling.md) and the language's error handling file.
3. For a write followed by a read or a completion check, read [read-after-write](references/core/client.md#read-after-write).
4. For non-trivial work, check the runnable examples and the matching pattern, then write a short Flow model of the Steps, state, and failure handling before code. The [design review](references/core/self-check.md#design-review) is an optional aid.
5. Then load what the task needs:

| Task | Core reference |
| --- | --- |
| New application, wiring, Workers, deployment rollouts | [application.md](references/core/application.md) |
| Flow boundary, start options, Flow timeout | [flow.md](references/core/flow.md) |
| Step graph, waits, decisions, durability, retries, heartbeats, locks | [step.md](references/core/step.md) |
| State, maps, indexes, search, hard-delete secrets, SQL projection of Attributes (Attribute Store sync) | [attribute.md](references/core/attribute.md) |
| Queues, approvals, moving pending messages | [channel.md](references/core/channel.md) |
| RPC design, transactions, cancellation from an RPC | [rpc.md](references/core/rpc.md) |
| Progress, LLM token streaming | [stream.md](references/core/stream.md) |
| Delays, deadlines, SkipTimer | [timer.md](references/core/timer.md) |
| Child Flows | [subflow.md](references/core/subflow.md) |
| Provider integrations, local or deployed Connector configuration | [connector.md](references/core/connector.md) |
| Client calls, reads, waits, lifecycle APIs | [client.md](references/core/client.md) |
| Errors, recovery types, diagnosis, operator recovery | [error-handling.md](references/core/error-handling.md) |
| Large values, BlobCache, whether to add a database, sensitive data and payload encryption | [data-handling.md](references/core/data-handling.md) |
| Pattern choice and skeletons, durable AI agents | [patterns.md](references/core/patterns.md) |
| Integration tests | [testing.md](references/core/testing.md) |
| Server behavior, search backends, deployment | [server.md](references/core/server.md) |
| dexcli, `dexcli dev`, visualization | [cli.md](references/core/cli.md) |
| Dex Web | [web.md](references/core/web.md) |
| FDG annotations, Summary and Display, Start forms, Actions | [FDG authoring](references/fdg-authoring.md) |
| Design review and handoff | [self-check.md](references/core/self-check.md) |

## Complete the vertical slice

A change includes typed inputs, outputs, and failures; Flow and Step definitions; every persistence schema entry; constructor-injected dependencies; registry, Worker, and Client wiring; and an application boundary. When the project has tests or the user asks for them, test durable behavior against a real Dex Server with unique Flow IDs and bounded waits instead of sleeps. When Dex Web renders the Flow, the graph reads from source: return analyzer-visible decisions from Execute, use targets the analyzer can resolve, compose Steps instead of subclassing them, and fix every `dexcli visualize` error diagnostic before handoff.

Before handoff, you may review the code against the [code check](references/core/self-check.md#code-check); list in the handoff only the items it does not meet.

## Proportionality

Keep the design proportional to the request. Do not add application-specific
tests, mocks, or framework dependencies only to satisfy a generic scaffold.
Existing or requested real acceptance remains required. Generate API packages
before resolving their imports. Use focused checks while editing, then
accept the change with the project's own build and test commands. Do not run
the same full gate twice in a row; act on its actual diagnostics instead.

## Links

- [Developer reference overview and compatibility matrix](https://docs.superdurable.io/reference/)
- The SDK reference for the application's language:
  [Go](https://docs.superdurable.io/reference/go/),
  [Python](https://docs.superdurable.io/reference/python/),
  [TypeScript](https://docs.superdurable.io/reference/typescript/),
  [Java](https://docs.superdurable.io/reference/java/), or
  [Rust](https://docs.superdurable.io/reference/rust/)
- [Dex Web](https://docs.superdurable.io/reference/web/), including the
  [Flow source analyzer](https://docs.superdurable.io/reference/web/flow-visualization)
- [CLI](https://docs.superdurable.io/reference/cli/)
- [Server](https://docs.superdurable.io/reference/server/), including
  [runtime data](https://docs.superdurable.io/reference/server/runtime-data)
  and [Step and Flow close](https://docs.superdurable.io/reference/server/step-and-flow-close)
- The runnable examples for the application's language:
  [Go](https://github.com/superdurable/dex/tree/main/examples/go),
  [Python](https://github.com/superdurable/dex/tree/main/examples/python),
  [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript),
  [Java](https://github.com/superdurable/dex/tree/main/examples/java), or
  [Rust](https://github.com/superdurable/dex/tree/main/examples/rust)
- [Connector catalog](https://connectors.superdurable.io/catalog.yaml) and
  [Connector library](https://github.com/superdurable/dex-connectors-library)
