---
name: dex-app-builder
description: Primary skill for all Dex work with source-available Dex. Use for Dex product, application, or workflow requests; for requests to learn, get started with, set up, or understand Dex; and for standalone SDK work that implements, debugs, tests, or operates code using the Dex SDK in Go, Python, TypeScript, Java, or Rust. Orchestrates business discovery, Flow and FDG modeling, the backend and its application API, and local verification with dexcli dev, plus the user's own UI, Dex Web, or released connectors when the user wants them. Not for official connector-library work.
---

# Dex App Builder

Build reliable Dex applications through Dex's public programming model. Keep
the user's experience in Dex terms and Dex APIs. A capability missing from a
higher-level library, such as an agent package, is not a missing API: build
it with the Dex SDK.

The toolchain is source-available Dex: the Dex SDK for the application's
language (Go, Python, TypeScript, Java, or Rust), `dexcli` (including
`dexcli dev` and the FDG analyzer behind `dexcli visualize`), Dex Server
(server-zd), Dex Web, and released official Connectors.

This file is mostly facts: how each Dex mechanism behaves in each case, and
its defaults. Read it fully. A design follows from these facts, one Must
rule, and the patterns that combine several facts for common business shapes.

## Session start and routing

Run the [Dex Skills version check](references/core/plugin-version-check.md)
once per session. Read the lifecycle hook status first. Run its fallback
only when the status is `unavailable` or absent. The check never blocks the
task.

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

A **Flow** is one durable run of a business process, with a stable Flow ID. It is a graph of **Steps**. Each Step has **WaitFor**, which returns the Conditions to wait on, and **Execute**, which does the work and returns a decision: the next Steps, or how the Flow closes. An execute-only Step has no WaitFor. Each successful Step method result is one commit of the Flow's durable state. A Flow can define **RPCs**: handlers that callers outside the Flow invoke to read or change its Attributes and Channels, and to start or cancel its Steps. They add Flow-specific interaction on top of the Client APIs, such as starting, reading, waiting for, and stopping a Flow.

| Concept | What it is | Key point |
| --- | --- | --- |
| [Flow](references/core/flow.md) | durable execution with identity, schema, and RPCs | one Flow per business lifecycle, named after the domain; another top-level Flow for a separate owner and lifecycle |
| [Step](references/core/step.md) | WaitFor plus Execute; options set durability, timeouts, retries, loads, locks | one recovery boundary; parallel Steps (GoToMany) for parallel work in one lifecycle |
| [Attribute](references/core/attribute.md) | typed durable state; AttributeMap for keyed instances | current state that later Steps, reads, or search need |
| [Channel](references/core/channel.md) | durable FIFO queue consumed by a waiting Step | queued commands, approvals, or events, which outside callers publish through an RPC |
| [RPC](references/core/rpc.md) | typed synchronous handler on a Flow | the application's write path into a running Flow |
| [Stream](references/core/stream.md) | best-effort, resumable messages | live progress, with durable state in Attributes |
| [Timer](references/core/timer.md) | durable delay Condition | wakes the Flow at a time, while the Flow timeout bounds the whole Flow |
| [SubFlow](references/core/subflow.md) | child Flow started from WaitFor | a child with its own lifecycle, history, or retries |
| [Client](references/core/client.md) | the application's boundary to Dex | starts, invokes, reads, waits for, and stops Flows |
| [Connector](references/core/connector.md) | released provider integration: ready-made Connector Steps (Query or Mutation) and Triggers | a provider integration that ships as released official modules |

The Components are the processes that run this model: [application (Worker and Client)](references/core/application.md), [server](references/core/server.md), [dexcli and local development](references/core/cli.md), [Dex Web](references/core/web.md).

## Facts

These facts hold for every Dex application, in every SDK. Defaults are the
current Server and SDK defaults; the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits)
and each SDK's [StepOptions reference](https://docs.superdurable.io/reference/go/step-options#defaults)
hold the current values and the option names. Per-language API names and
signatures are in the SDK reference for the application's language.

### Commits and isolation

- Each WaitFor, Execute, RPC, and timeout handler call commits at most once. Its staged Attribute writes, Channel message deletes, and Channel publications commit together with its result, and only when the method returns successfully and the result passes validation; a method canceled or stopped before that commits nothing. A read-only RPC, one whose result has no [durable effects](#rpcs), commits nothing.
- Hard-delete Attribute values follow the same rule: a value becomes readable only when its method's result commits. Dex uploads it before the commit; it deletes an upload whose result did not commit, and a superseded object after its overwrite commits, in the background, so object storage can still hold a value that no longer reads until that cleanup runs.
- A commit is atomic but not isolated. Each method reads its own snapshot, and Dex does not check at commit time whether what it read is still current, so two methods that read the same value and write it back can overwrite each other. Three Dex mechanisms order such writers, and only for the writers that use them:
  - The same Attribute lock: Steps that need it queue, and an RPC that needs it fails at once with a lock conflict ([Locks](#repeated-execution-snapshots-and-locks)).
  - A Channel to one consuming Step: writers publish, and only that Step reads and writes the value. A publishing caller learns only that its message is stored.
  - A pending token message: transactional RPCs that each delete the token they read and publish its replacement conflict at commit. The later one fails with not-found and commits nothing, and its caller can call again.
- A publish only appends, and each AttributeMap instance is stored separately, so concurrent publishes to one Channel, and writes to different instances, need no lock and lose nothing.
- External calls are outside the commit, and Dex cannot roll them back. Stream writes and heartbeats are outside the commit too. Cancellation is cooperative, so a canceled or timed-out attempt can still complete its external call after later work, such as compensation, has started.
- A Client call from a handler into its own Flow is not part of the handler's commit, reads the latest committed state instead of the handler's snapshot, and can fail on the handler's own locks.
- A canceled method loses only what it has not committed. What an earlier method of the same Step committed, such as WaitFor's Attribute writes, stays; the messages its Wait consumed return or are dropped as described under [Conditions](#conditions-and-channel-messages).

### Repeated execution, snapshots, and locks

- Every Step method and timeout handler may run more than once: on retry, after a Worker crash, after an ASYNC rollback, and as overlapping attempts after a cancellation. An RPC handler runs again only when its caller calls again.
- Dex reserves a Worker slot, then records the attempt start, which fixes the snapshot and takes the declared locks in one step. A method therefore gets its snapshot only after it holds all its locks.
- All retries of one WaitFor, Execute, or timeout handler call reuse the snapshot of its first started attempt. A retry does not see writes made after that, so retrying never waits for someone else to fix the state. Each RPC invocation that runs its handler reads a new snapshot, even with a repeated request ID.
- A Step holds its declared locks from its first started attempt until the method's result commits or its retries run out, the Step is canceled, or the Flow closes; a handler that has returned still holds them until its result commits. Locks are held through external calls, retry backoff, and the queue wait of a retry. No command releases one lock. Under the default retry policy, a failing locked Step can hold its lock for the whole [retry total duration](#workers-attempts-and-time-bounds) plus its last attempt. A Handler-policy Flow timeout alone does not cancel the Step.

| Step attempt state | Snapshot and locks |
| --- | --- |
| First attempt not yet handed to a Worker | none yet; later Steps that need the lock queue behind it, and RPCs still take it |
| Returned to pending, such as after a Worker declined its Step type, or a retry waiting in backoff or in the queue | keeps its snapshot and locks, even when no Worker can run it |
| Under ASYNC durability, an attempt start lost on a Server node crash | the same attempt number can run again |

- A Step that needs a held lock queues and waits; it does not fail. It runs when all its locks are free and it is first in line for each. A method takes all its locks at once, so locks never form a deadlock cycle.
- An RPC never waits for a lock. When a Step or another RPC holds a lock it needs, the RPC fails at once with a lock conflict. Neither Dex nor the SDK retries it; the caller retries with backoff or reports busy. A lock conflict runs no handler and records nothing, so the caller may retry with the same request ID. When the lock is free, the RPC goes ahead of queued Steps.
- Dex does not infer locks from what a handler reads or writes. A read or write without a declared lock bypasses the lock. An Attribute, AttributeMap, or Channel definition can declare that every write or publish requires a declared lock; the Worker then fails a WaitFor, Execute, or RPC call that wrote without declaring it. The Worker checks writes against the locks registered for the handler, not against per-call lock overrides, so an override that drops the lock still passes the check. Each Client and Worker checks against the definitions it registered. The check does not cover the timeout handler.
- WaitFor and Execute each have their own snapshot and lock set; they never share one commit. No lock is held while the Step waits between them.

### RPCs

- Neither Dex nor the SDK Client retries an RPC. A caller that calls again makes another invocation. With the same request ID, Dex applies its Flow changes at most once: a transactional or locking RPC runs its handler once, and any other RPC runs its handler again but returns the recorded reply (the tables below give the exceptions). The SDK sends a new request ID per call unless the caller sets one. An RPC result has **durable effects** when it changes the Flow (writes, publications, message deletes or moves, Step starts or cancels) or records events.

| RPC kind | Behavior |
| --- | --- |
| Read-only: no lock, not transactional, no durable effects | Changes nothing. It also succeeds on a closed run the Server still retains, so its success does not prove the Flow is running. |
| Transactional, including every RPC that holds a lock | Its reads and writes commit as one atomic operation, and Dex checks at commit that each message it deletes is still pending. |
| Any RPC that locks, is transactional, or writes, publishes, or moves Steps | A new invocation needs an active run. On a closed run, a transactional or locking RPC fails with not-found before its handler runs; any other RPC that returns durable effects fails at commit, after its handler ran. A recorded request ID of a transactional or locking RPC still replays its reply after the run closes, while the run is retained. During a [pending close](#flow-timeout-completion-and-cancellation), some of these RPCs fail with FLOW_CLOSING and others commit without their Step starts. |

- An RPC result can return an output, write or delete Attributes, publish, delete, or move pending messages, start Steps, and cancel Steps. An RPC has no WaitFor and never waits for a Condition.
- Dex compares request IDs only, not RPC names, inputs, locks, or loads, so give unrelated logical operations distinct IDs. A call that reuses a request ID with a different input gets no error; the table below gives its outcome.

| RPC with an already recorded request ID | Behavior |
| --- | --- |
| Transactional or locking | Returns the recorded reply without running the Worker; concurrent calls with the same ID share one execution, including its failure. |
| Other, returning durable effects | Runs the handler again, repeating its external calls; the commit returns the recorded reply and applies no second effects. Two such calls that run concurrently both run their handlers; the first commit is recorded, and the other call returns that reply and applies nothing. |
| Other, returning no durable effects | Runs the handler again and returns its new output; nothing is recorded. |

- A handler that fails without committing records nothing, so a retry runs it again. A caller-side deadline or cancellation, or UNAVAILABLE after the Server's 60-second commit budget of a transactional or locking RPC, leaves the outcome unknown: the RPC may still commit. On a run over its state budget, an RPC that changes no Flow state keeps no reply or request ID.
- Each run keeps only its newest request IDs, one window shared by all its RPC names and its other recorded requests, such as direct Attribute writes (see [Default limits](#default-limits)); an older ID runs as a new request. A new run starts with none.
- The RPC timeout from the RPC's registration options, 60 seconds by default, bounds the dispatch to a Worker and the handler's execution; the SDK Client has no per-call override. The commit of a transactional or locking RPC has its own budget, and a caller deadline bounds only how long the caller waits.
- An RPC fails with UNAVAILABLE when no Worker takes it within the dispatch wait, 10 seconds by default, and with a named not-registered error when the connected Workers do not support its RPC name; a Step attempt waits for a Worker instead.
- An RPC can cancel Steps by Step type across the Flow. Dex picks the Steps to cancel when the RPC commits, not from the handler's snapshot, so a selected Step may already have committed its result. The handler cannot know the outcome; a later Step reads committed state. A canceled Step cannot commit anything more, such as its final text. Cancellation selects by Step type only, so it also cancels the matching Steps of unrelated work in the same Flow, such as another conversation turn, unless only one such Step is active at a time or each unit of work runs in its own Flow.
- A successful RPC that publishes confirms only that the message is stored, not that a Step has handled it.

### WaitFor and Execute

- WaitFor returns the Conditions to wait on. Its result can also write Attributes, publish and delete Channel messages, and write Step execution local values, which pass a value from WaitFor to Execute within one Step execution. WaitFor can call external systems, such as a read-only, idempotent provider read whose value sets a Timer; a value it reads persists only through what its result writes.
- WaitFor has its own retry policy. When its retries run out, its failure policy fails the Flow (the default) or proceeds to Execute with the unchanged input; that Execute sees the failure and its recovery error in its Context and can route it. WaitFor itself returns no decision such as GoTo or ForceFail; to let Execute route a permanent error at once, WaitFor stores it in a Step execution local value and returns a Wait that is ready immediately. A retried WaitFor reuses its [snapshot](#repeated-execution-snapshots-and-locks), but an external read made again can return a different provider value. Only the committed WaitFor result defines the Wait.
- Execute runs when the Wait is satisfied and returns the decision. When its retries run out, its failure route runs if the Step has one: a recovery Step that receives the failed Step's input unchanged (null after a failed timeout handler) and reads the failure from its Context; otherwise the Flow fails.
- A Wait is Until one Condition, AllOf, AnyOf, or AnyCombinationOf; a WaitFor can also skip the wait and go straight to Execute. AnyCombinationOf is ready when every Condition of any one combination is ready. Condition IDs are unique within a Wait, and AnyCombinationOf needs one on every Condition.
- Only Conditions wake a waiting Step: an Attribute write does not. A change that must interrupt a wait publishes to a Channel that the Wait includes. A running Execute keeps its snapshot, so reading a control Attribute again inside the same call or its retries never sees a later write; a new Execute, such as the next GoTo of a loop, does.
- A SubFlow Condition starts its child Flow from WaitFor.
- A failure route can tell the work certainly did not run only when the recovery error reports that no attempt started, which SYNC durability can report; ASYNC failures always report that the work may have run. A failure that may have run may still have taken effect at the provider.

### Conditions and Channel messages

- Dex checks a Wait when it starts, then again after each committed change that can satisfy it: a Channel change, a fired or skipped Timer, an ended SubFlow, or a graceful completion request. Messages queued before the Wait started count toward its Conditions at once; otherwise the Condition whose event commits first wins.

| Channel Condition | Ready when | Consumes |
| --- | --- | --- |
| ForOne | one message is pending | one |
| ForN(n) | n are pending | n |
| AtLeast(n) | n are pending | every pending message |
| AtMost(n) | at once, even when empty | up to n |
| AtLeastAtMost(min, max) | min are pending | from min up to max |

The Condition names are Go's; the SDK reference for the application's language has the others.

- When several Conditions are ready in the same check, AnyOf selects Timer, then Channel, then SubFlow, then graceful completion, the last so that a ready message wins over it; within one kind, declaration order. Across kinds the argument order has no effect. AnyCombinationOf selects the first satisfied combination in declaration order. Ties happen only when the Wait starts or one commit changes several Conditions; a Timer fires in its own commit, so between a Timer and a message the first committed event decides.
- Only the selected Condition consumes messages: in AnyOf, the selected Channel Condition; in AnyCombinationOf, the selected combination. Other Conditions' messages stay queued.
- When the Timer wins, no message is consumed. While the Flow is running and not in a [pending close](#flow-timeout-completion-and-cancellation), a message published after that is still accepted if the Channel has room, stays pending, and counts toward the Channel's limit, and the RPC that published it returns success. A reply that commits after the Timer won but before the deciding Execute takes its lock is pending when that Execute runs; only an Execute that checks the Channel under the same lock the replying RPC takes sees every reply the RPC admitted.
- Message counts identify neither distinct senders nor distinct business decisions: a repeated message from one sender counts again. Distinct senders need their own Conditions or a record keyed by sender.
- Execute reads which Condition fired from its Context: whether the Timer fired, and the messages each Channel Condition consumed. The graceful completion result reports completed whenever a request exists, even when a Channel won, so check the Channel results first.
- When a Step decision or an RPC cancels a Step before its Execute commits, the messages it consumed return to the front of their Channel in their original order. The canceled Execute may already have seen them, so a message can be handled twice.
- When the Flow closes, every Step still active is canceled and the messages it consumed are dropped instead. This holds for every close: a StopFlow that cancels, fails, terminates, or force-completes, a Flow timeout under Fail or Cancel, a failure that closes the Flow, a ForceComplete, ForceFail, or successful ForceCompleteIfChannelsEmpty decision, and a termination by ID reuse or by a parent's SubFlow restart. Graceful completion cancels no user Step; it cancels only a running timeout handler when it begins. Messages still pending when the Flow closes stay in the closed run. What a publish does while the Flow closes is under [pending close](#flow-timeout-completion-and-cancellation).
- Several Steps may wait on one Channel. Dex offers a new message to the waiters in the order their waits started. A wait that needs more messages than are queued is skipped, so a wait for n messages can wait forever while one-message waits take each message. Waiters that take one message each and may take any message, such as a worker pool, can share a Channel; which one receives a message depends on timing. One message goes to one waiter; Dex has no broadcast.
- A graceful completion Condition wakes only the Waits that include it, and a Wait has at most one.

### Timers

- A Timer's deadline is the Server timestamp of the WaitFor result that declared it, plus its duration. Durations are whole seconds. The Timer is durable once that result commits. A Timer expresses elapsed seconds, not a calendar: a business-hours cutoff or a monthly date is computed by the application, and each next occurrence becomes a new relative Timer. Handler code sees only whether a Timer fired; the Server's Flow state read shows a waiting Timer's firing time, rounded up to whole seconds, for inspection. A deadline that business logic compares with the clock, such as at admission, is one the application computes and stores in an Attribute.
- A Timer never fires before its deadline unless SkipTimer skips it. It can fire late when the Server is busy or fails over; a periodic full scan, every hour by default, catches a Timer whose wake-up was missed.
- Only Timer Conditions and the Flow timeout wake a Flow at a time.
- SkipTimer makes a waiting Timer fire at once through the Client. The Flow timeout is a Timer too: skipping it fires the Flow timeout now and applies its policy; it does not remove the timeout. See [timer.md](references/core/timer.md#skip-a-timer).

### Flow timeout, completion, and cancellation

| Flow timeout policy | When the timeout fires |
| --- | --- |
| Fail | The Flow closes as failed. |
| Cancel | The Flow closes as canceled, with no retry. |
| Handler | The timeout handler runs and returns a decision like Execute. A closing decision closes the Flow; GoTo or DeadEnd keeps it running with no Flow timeout left. When its retries run out and it has no failure route, the Flow fails. Handler requires a registered timeout handler; the SDK rejects this policy when the Flow has none. |

- A Flow timeout of 0 means none. With a positive timeout, the SDK defaults to Fail, or to Handler when the Flow has a timeout handler.
- The Flow timeout and its policy are fixed at start; of the start options only the Flow configuration can change later. A limit that can move needs a Timer, such as the [deadline watchdog](references/core/patterns/polling-and-timers.md#deadline-watchdog).
- The Flow timeout is a [Timer](#timers), so it can fire late, and the close cancels running handlers cooperatively. When a Fail or Cancel timeout is processed, Dex begins closing and stops scheduling new Steps. Neither policy forcibly stops external calls already under way, and after such a close no application code of the Flow runs, so an effect still in flight is reconciled only outside the Flow. Under the Handler policy, the timeout handler runs while the Flow is still open.
- Without a Flow timeout, nothing ends the wait of a Step that no Worker takes (see [Workers](#workers-attempts-and-time-bounds)), or of a SubFlow without its own timeout.
- The timeout handler has Execute semantics: its own method timeout, retries, heartbeat, locks, loads, and failure route. A run makes one logical timeout handler call, whose attempts can repeat; there is no Flow-level retry. Active Steps keep running while it runs and can start successors until it commits a closing decision. When it commits ForceComplete or ForceFail, Dex starts closing the Flow and cancels the other Steps. GracefulComplete from it waits for every other active Step to end.
- A Flow start rejects a positive start delay and a Flow retry policy.
- GracefulComplete ends the deciding branch and requests completion; a StopFlow can request it too. Completion starts when no Step other than the timeout handler is active, and a running timeout handler is then canceled; a later DeadEnd does not withdraw the request, and no new Step starts after completion begins. Graceful completion never checks Channels: while a completion is requested, publishes are still accepted, and when the last other Step ends or is canceled, the Flow completes in that commit and leaves pending and returned messages in the closed run. ForceCompleteIfChannelsEmpty is the close that checks Channels. A requested graceful completion is not a pending close.
- Dex never closes a Flow because no Step is active. Without a pending graceful completion, the Flow runs until its timeout, a stop, a failure that closes it, or a decision that closes it.
- ForceComplete and ForceFail begin closing the Flow and cancel the active Steps when the decision commits. Every close except a StopFlow cancel, fail, or terminate, and a termination by ID reuse or a SubFlow restart, records the terminal status only after admitted transactional or locking RPCs finish and pending Attribute Store writes are written or dropped. With neither pending, the close records the terminal status at once.
- **Pending close** is the state between such a close decision and its recorded terminal status. Every Step is already canceled, the run reads as RUNNING, and WaitForFlow keeps waiting. During it:
  - a new transactional or locking RPC, a direct publish or message delete, a completing StopFlow, and a new Step-completion or Attribute-match wait fail with FLOW_CLOSING, and an Attribute-match wait already waiting and not matched ends with that error;
  - an RPC whose result publishes to a Channel fails as a whole with FLOW_CLOSING, whatever its kind;
  - any other RPC, including a transactional one admitted before the close, commits its Attribute writes and returns success, but the Steps it starts or cancels are ignored;
  - direct Attribute writes are still accepted.
  See [Pending close](https://docs.superdurable.io/reference/server/runtime-client#pending-close).
- ForceCompleteIfChannelsEmpty checks the named Channels atomically when the decision commits. A Channel is non-empty while it has pending messages, including messages a canceled Step returned, or a message consumed by a Step whose Execute has not finished. If all are empty, the Flow begins completing; otherwise the decision starts the given fallback Steps.
- Cancellation is cooperative. The SDK signals the handler, and the handler must observe it; a handler that ignores it keeps running and its external calls still happen. Dex discards a canceled Step's later result, retries, and failure route. A decision or RPC never cancels the Step it starts.
- StopFlow cancel, fail, or terminate closes the run when the stop commits and starts no more application code: no decision, failure route, or cleanup. Handlers already running get a cancellation signal and can keep running. Such a stop that commits during a pending close replaces it: the run closes with the stop's status and keeps no output. StopFlow can instead request a graceful completion or force-complete the Flow, with an output, like the Step decisions of the same name; a graceful completion request lets active Steps finish and wakes the Waits that include the graceful completion Condition.

### Starting a Flow and ID reuse

- StartFlow returns once the Server accepts it, and writes the initial Attributes atomically with the Flow's creation, so the first Step sees them.
- The Flow ID is the business identity. A Flow ID with no run always starts.
- The ID reuse policy decides by the **latest run** only: whether it is running and how it closed, never what it saved. The tables below decide identity conflicts; a start can still fail for other reasons, such as invalid input or a limit. A run that recorded a business failure and then completed is COMPLETED.

| ID reuse policy | Latest run RUNNING | Latest run COMPLETED | Latest run FAILED, CANCELED, or TERMINATED |
| --- | --- | --- | --- |
| Disallow reuse (the default, also when unset) | reject | reject | reject |
| Allow if previous failed | reject | reject | start a new run |
| Allow if no running | reject | start a new run | start a new run |
| Terminate if running | terminate it, then start a new run; never rejects | start a new run | start a new run |

- Every start carries a Request ID; the SDK fills a random one when the caller omits it. Dex compares the Request ID with the one that started the **latest** run, and compares nothing else: not the input, not the options.

| Request ID compared with the latest run's | ignore-already-started off | ignore-already-started on |
| --- | --- | --- |
| Same: a retry of that start; the reuse policy is not applied | already-started error, under every policy | returns that run's Run ID, under every policy |
| Different, or omitted | the reuse policy decides; a rejection is an already-started error | the same: ignore-already-started applies only to the same Request ID |

- StartFlow decides repeats itself, atomically per Flow ID: the reuse table and the Request ID table above give the outcome of every start, including concurrent ones.
- So the retries of one logical start are recognized only when they carry its Request ID, such as an operation ID the caller stores or one derived from the complete logical request; a different logical start with an identical payload is told apart only by a different Request ID. After a newer run starts under the same Flow ID, a late retry of an older start counts as a new request and goes to the reuse policy.
- An already-started error carries no Run ID. Read the latest run with DescribeFlow or a Client Attribute read; what identifies the request that started it is only what its starter stored, such as an initial Attribute.
- Start deduplication lasts only while a run of the Flow ID exists. A closed run is retained for 7 days by default, then purged; after that the Flow ID starts again even under Disallow reuse, and the new run repeats its external effects unless the receiver drops the repeated key. Deduplication that must outlive retention lives in the application's own record or at the receiver.
- Terminate if running is the only policy whose start closes a running run. A retry with the same Request ID never terminates the run it started.

### Runs, reads, and outputs

- The Server creates the Run ID; every new run under a Flow ID gets a new one. Attributes, hard-delete values included, Channel messages, and request IDs belong to one run: a new run under a reused Flow ID starts with only its start input and initial Attributes.
- History never limits or fails a run, so a long-lived run never needs to close and restart to shed history. Dex keeps about the newest 50,000 history events of each run by default (5,000 on a single-node Server, such as under `dexcli dev`), and a Flow configuration can set its own window. Older events are pruned without affecting the Flow, and only active state counts toward the [state limit](#default-limits). History reads and time travel reach only inside the window, so a record the business must keep lives in Attributes or outside Dex.
- The Context carries the Flow start time, the same for every Step, retry, and RPC in one run. A Step method's Context also carries the first-attempt time of its current call; an RPC's Context has none, so an RPC admission time is the handler's clock reading or a time the caller supplies. A clock reading can change on every retry.
- A Client Attribute read is answered by the Server without a Worker. One read takes several keys and exact map instances, all from one committed state. An SDK Client read cannot enumerate a map, and no Attribute read returns Channels. Client Attribute reads and DescribeFlow target the current or latest run. A retained closed run stays readable until it is purged. An Attribute-match wait compares one scalar Attribute and targets only the active run; a Step-completion wait waits for one Step execution. A StopFlow without a Run ID stops the running latest run and fails with not-found when that run is closed.
- A Client Attribute read or read-only RPC started after a successful write sees the committed state of the same run, including that write unless a later write replaced it. This does not cover a reused Flow ID, a failed or unknown write outcome, the Steps the write triggers, or projections.
- Only a COMPLETED Flow keeps outputs: one for each GracefulComplete or ForceComplete decision, or completing StopFlow, that carried one. A failed, canceled, or terminated Flow has no output, even after an earlier GracefulComplete output; so does a timed-out Flow under the Fail or Cancel policy. A timeout handler that completes the Flow keeps its completion output.
- Step outputs live only in history, and neither a Step-completion wait nor an Attribute-match wait returns one. A result or status a user must see needs a read path: a result Attribute, a read-only RPC, or the Flow's completion output.

### Time travel

- Time travel is a privileged Client operation, also available in Dex Web and dexcli. It starts a new attempt of one run from a point inside the run's [history window](#runs-reads-and-outputs), or from its beginning, which stays selectable after pruning. It keeps the Run ID. Steps after the point run again, with their external calls; RPC handlers never run again.

| State | After time travel |
| --- | --- |
| Attributes, except hard-delete Attributes | the point's values |
| Channel messages, Step executions that were waiting, ready, or running, Step execution numbering, locks, and waits | the point's state; an attempt that was running runs again |
| Wait Timers | keep their original fire times, so one already past fires at once |
| Flow timeout | still pending at the point: restarts with its full duration; already fired: keeps its restored outcome |
| Retry total duration of a restored method call | restarts |
| Hard-delete Attributes | unchanged: each keeps its current value or absence, so a re-run Step can read a newer value than the point had, or none |
| Request IDs recorded for the run | kept, also for requests whose effects are not applied again |
| External effects and child Flows | not undone; a child follows the SubFlow reset policy (see [SubFlows](#subflows-and-child-flows)) |

- Write reapply is on by default and can be skipped. It applies the recorded input that reached the run after the point, at once, within the time travel and before any re-run Step: Attribute writes other than hard-delete writes, publications, message deletes, SkipTimer calls, and RPC results with their Step starts and by-type Step cancels. A by-type cancel acts on the restored state, so it can cancel other Steps than it did originally. Input the restored state no longer accepts is skipped, such as a transactional RPC whose deleted message no longer exists, which is skipped as a whole.
- A Step execution that exists at the point keeps its Step execution ID. A Step created after the point gets the next number of its Step type in the new creation order. Steps started by reapplied RPC results are created first, and a different branch order or changed code also changes the order, so a Step execution ID created after the point can name other work than it did before.
- A value a Step generated and committed after the point, such as a random operation ID, is gone, and the re-run Step generates a new one. Values committed before the point, the start input, and reapplied input stay.
- The Context does not expose the run attempt; an effect that must repeat after time travel needs an application-owned discriminator.

### IDs across repeats

| Value | Retries and overlapping attempts of one call | Time travel | A later run of the same Flow ID |
| --- | --- | --- | --- |
| Flow ID | same | same | same |
| Run ID | same | same | new |
| Step execution ID | same | same for a Step execution that exists at the point; one created after it can get an ID that earlier named other work | numbered from the start again, so the earlier run's IDs recur |
| Start input and initial Attributes | same | same | the new start's |
| A value committed to an Attribute before the call | same | same when committed before the point; generated again when its Step runs again | not carried over |
| Attempt number, clock reading, fresh random value | changes | changes | changes |

- An RPC handler runs again only when its caller calls again, and a caller that repeats a call can repeat its input, such as its own operation ID. An RPC has no Step execution ID, so its key comes from its input. Time travel applies recorded RPC results again without running their handlers.
- A read at the provider before the write does not deduplicate on its own: two overlapping attempts can both read "absent".
- A key that stays the same across all three columns is derived from business data: the start input, an RPC input, or state committed from them, such as an order number with the action's name and an iteration count kept in an Attribute. The Flow ID with a fixed name of the call is such a key when the Flow makes that call once. Add the Run ID when a later run under the same Flow ID is a new logical operation.
- A Step execution ID stays the same across time travel only for a Step type whose executions are always created in the same order.
- A later Step or the timeout handler that reconciles an effect can rebuild a key derived from business data. The timeout handler receives no input.

### SubFlows and child Flows

- The Server derives a SubFlow ID from the parent Flow ID, the waiting Step's execution ID (its Step type and a per-run counter of that Step type), and the Condition index. A SubFlow ID is therefore not caller-chosen: a child that people look up, retry, reopen, or stop by its own number is usually a top-level Flow whose business number is its Flow ID, or the application keeps a mapping from that number to the child.
- A Step type that starts a SubFlow must not contain `-`; Dex fails that WaitFor attempt without retry.
- Dex first compares the Request ID it generated for the SubFlow start. When it matches, Dex attaches to the running child or returns its final result, under every policy. Otherwise the SubFlow reuse policy decides:

| Existing child | Attach | Restart if previous exits abnormally (default) | Always restart |
| --- | --- | --- | --- |
| None | start | start | start |
| Running | attach | attach | terminate it, then start |
| COMPLETED | return its result | return its result | start |
| FAILED, CANCELED, or TERMINATED | return its result | start | start |

- A later run of the same parent Flow ID counts Step executions from the start again, so it derives the same SubFlow IDs. Under the default it reuses the earlier running or completed children; Always restart terminates a running one. Different Step executions or Condition indexes within one parent derive different child IDs; only when a later parent run derives the same ID and both versions of that child must keep running does the later run need a different parent Flow ID.
- A child keeps running when its parent Step is canceled or its parent Flow closes; Dex does not stop it. This holds for SubFlows and for top-level children, except that Always restart, or a time travel of the parent under a SubFlow reset policy that restarts or fails the child, terminates a running SubFlow. The default reset policy attaches to an existing child and leaves it running.
- The serialized outputs a child delivers to its parent are limited; see the [limits table](#default-limits).
- A delivered child result makes the parent's SubFlow Condition ready; it does not prove the parent consumed it or completed later work.
- A child can close without running its own reporting code: a StopFlow cancel, fail, or terminate starts no more of its code, a failure after a Step exhausts its retries runs only its failure route, and a Fail or Cancel Flow timeout closes it. A SubFlow's parent still receives the terminal status as the Condition's result; the parent of a top-level child must read it.

### Joins

- A Join is a named ChannelMap that counts committed branch result messages per round; it does not infer one completion per branch. The branch's designated reporting Step, its recovery Step, or an RPC reports once, with its method's commit, so a retried report counts once; a branch that reports twice, or a report repeated by another logical call, counts twice. One round's results never satisfy another round's join, unless a round key is reused. A replacement branch that reports in the same round adds one result more than the round's branch count: a join for all branches then takes the first results in commit order, which can include a canceled branch's earlier report and leave a live branch's result pending.
- Canceling a branch produces no result and runs no failure route; a report committed before the cancel stays. Dex picks the Steps to cancel at commit time, so a canceller cannot safely report for a branch from its own snapshot.
- A decision that cancels sibling Steps cancels the active Steps of the named Step types that were started by the same Step execution that started the deciding Step. The later Steps of a multi-Step branch and a branch's recovery Step, which the failed branch Step started, are not those siblings. Steps started by all invocations of one RPC name are siblings of each other. A partial join consumes only the results it selected. Uncanceled late branches keep reporting, and their results stay pending until a reset consumes them or the Flow closes. A round whose branches were canceled cannot be reset, because they never report.
- Waiting for all branches of an empty list is satisfied at once; waiting for the first or n of an empty list is an error in WaitFor, which then follows WaitFor's retries and failure policy.

### Attributes, loading, and secrets

- Ordinary Attributes and Channel sizes load automatically. AttributeMap values and pending messages load only when the handler's options select them: StepOptions for a Step, the registration or invocation options for an RPC, and the timeout handler options. Reading one that is not loaded fails. A lock loads nothing. A read of pending messages comes from the snapshot and does not show the handler's own staged publishes and deletes.
- A hard-delete Attribute lives only in object storage, and history keeps only a reference. Each write stores a new object of the run, and Dex deletes the old one after the overwrite commits, so the value is the last committed write. A rejected or uncommitted start or write leaves the stored value unchanged. After a committed delete or null, the value is gone for good. [Time travel](#time-travel) never changes a hard-delete Attribute.
- Ordinary writes can stay in retained history after an overwrite or a delete, subject to the run's [history window](#runs-reads-and-outputs) and value retention. A delete is not immediate erasure, and history is not a permanent archive.
- A short-lived secret the Flow itself must hold can go in a hard-delete Attribute and be deleted after use. A released Connector resolves its credentials from project configuration, outside Flow state.
- Payload encryption leaves indexed and plaintext-declared Attributes, names, and IDs as plaintext on the Server. Search compares indexed Attributes. Attribute-match waits, Action conditions, and typed Attribute Store columns need a plaintext value; declaring an Attribute plaintext does not index it.
- Search results lag committed writes: a miss does not prove a committed value is absent. A Flow decision reads its own state, not search.
- Attribute Store sync projects the latest Attribute values into the application's PostgreSQL tables in the background. Commits never wait for it; a close does, except a StopFlow cancel, fail, or terminate and a termination by ID reuse or SubFlow restart (see [Flow close](#flow-timeout-completion-and-cancellation)); a write can repeat, entries still pending after its retry budget are dropped without affecting the Flow, and it does not backfill old values or a new store. Treat the table as eventually consistent.
- Index keys share the Server's finite search capacity; slot limits depend on the search backend, some backends cap slots per type, and the slot registry has no delete. Renaming an index key does not backfill existing Flows.

### Streams

- A Step's Stream write is sent at once, outside the commit, and a later failure does not roll it back. Each write counts as a heartbeat. When the Stream Store is unavailable or full, or the message is larger than its size limit, the message is dropped and the Step does not fail. All attempts of one Step execution write under the same source, and a retry runs the handler again, so a reader can see repeated or missing deltas and replaces live text with the committed state after a stop, a completion, or a Stream loss. A write can reach readers before the progress it reports commits.
- Streams live only in Server memory and are lost on restart or failover. Capacity is shared per Flow type and Stream name, and old messages are trimmed.

### Workers, attempts, and time bounds

- A Worker process can restart or be replaced at any time, and a later Step or RPC may run on another Worker. What survives is what Dex stores, Attributes, Channel messages, Timers, and the Flow timeout, and what an external durable store holds; Worker memory, a local sleep, and an in-process scheduler are lost.
- Dex routes a task by task queue and Flow type, not by Step type. Within one task queue a Flow type name maps to one Flow definition, and Flow types must be unique within one registry. Sticky routing is best effort: after a short wait, or when the original Worker disconnects, any Worker can take the task. A later Step or RPC may run on another Worker.
- Dex does not replay handler code. An open run continues on the code deployed now: it finds its code by Flow type, Step type, and RPC name, and its state by Attribute and Channel names, each time a method starts; a Step that already waits keeps the Conditions its WaitFor returned and runs the Execute deployed when a Worker takes it. After a rename, a Step of the old Step type waits for a Worker that registers it, a call to the old RPC name fails with a not-registered error, and a renamed Attribute or Channel does not read what the run stored under the old name. An open run is not pinned to a Worker build, so every Worker that serves it applies its own business rules to it; a run that must keep earlier business behavior stores a behavior version, and the code defines what an absent version means. A run's task queue is fixed at start, and new runs can move to another task queue or Flow type while earlier Workers keep serving the old one.
- Under SYNC durability one attempt number runs on at most one Worker. If the Worker crashes or disconnects after delivery, or a Server restart or failover interrupts the attempt, it fails and uses a retry. An attempt returns to pending without using a retry, keeping its attempt number, only in these cases: a Worker declined it as an unknown Step type, or the task was not delivered, including a task that reaches a Worker whose stream is closing in a clean shutdown. Every other failure uses a retry, even one Dex reports as not run. A drain lets running handlers finish and commit until the drain deadline, a Worker option; an attempt still running at that deadline fails and uses a retry. Under ASYNC durability a Server node crash can run the same attempt number again.
- A Step that no Worker takes waits in the durable task queue. It does not fail and uses no retry; only a closing Flow timeout, a stop, another close, or a cancellation of that Step ends that wait.
- The method timeout and the heartbeat timeout start when the task is sent; waiting for locks or in the queue does not count.
- The heartbeat timeout is 1 minute by default. An attempt that sends no heartbeat and writes no Stream for that long times out, and raising only the method timeout does not move that deadline. A call that can stay silent longer needs a higher heartbeat timeout or heartbeats from a background task.
- A heartbeat can carry a checkpoint value. The next attempt receives the last value Dex stored, which can be older than the last one sent, so it is resume state, not proof of an external effect.
- Step method defaults: method timeout 2 hours per attempt; retry backoff 1 second, doubling up to 100 seconds; retry total duration 4 hours, with unlimited attempts within it; durability SYNC. Durability resolves from the method's StepOptions override, then the Flow configuration default, which can be ASYNC, then SYNC.
- The retry total duration counts from the first delivery; if the first attempt returns to pending without running, the clock restarts. Dex checks it only after an attempt fails, before scheduling another retry, and never stops a running attempt. An already scheduled retry may wait in the queue past the budget and still run for a full method timeout. With Workers promptly available the overrun is about one method timeout; queueing can make it longer. Retrying can also stop up to one backoff interval early.
- SYNC durability, the default, stores the attempt start before the Worker receives the task and stores the method result before downstream work starts. ASYNC may start the attempt and downstream work before those writes are stored; a Server node crash can lose the result and rerun the method and the work started from it.
- Concurrent executions in one Worker process share the registered Flow and Step objects, so a mutable field on one of them is shared by every invocation; per-invocation data comes from the Context and the input.
- An error that a Step method or timeout handler returns is retried until the retry budget runs out, even when the provider confirmed it is permanent. No SDK has an error type that stops retrying; a handler can change only the delay before the next retry (RetryAfter), and a RetryAfter that lands past the retry total duration ends retrying. A result the Server rejects, such as a value over the [size limit](#default-limits), fails the attempt without retry.

### Default limits

The Server rejects a request that would exceed a limit, except the request ID limit, which forgets the oldest IDs instead. Each run records these limits when it starts, so a changed value applies only to runs started after the node restarts with it; the exception is the concurrent-wait limit, which each node applies from its current configuration. In this table a "direct write" or "direct publish" is a FlowService request outside a Step or RPC, such as one from Dex Web or dexcli. The values are current defaults; the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits) has the option names.

| Limit | Default | When exceeded | Workarounds |
| --- | --- | --- | --- |
| Active state of one Flow run | 64 MiB | A start, direct write, or RPC that grows active state over the budget fails with RESOURCE_EXHAUSTED; retrying without shrinking the request or freeing state fails again. Deletes, consumption, and requests that do not grow state still run. A Step's heartbeat or completion that grows it fails the attempt without retry. | Keep growing collections in a [sequentially chunked AttributeMap](references/core/patterns/flow-shapes-and-state.md#sequentially-chunked-attributemap) and delete or archive old chunks. Bound a queue with [back pressure](references/core/patterns/parallel.md#back-pressure). Data that cannot fit stays in an external store, and the Flow keeps references. |
| One value, such as an Attribute value or a Channel message | 16 MiB | A start, direct write, or RPC with a larger value fails with INVALID_ARGUMENT, and so does every retry of the same value. A Step heartbeat or completion with a larger value fails the attempt without retry; its failure policy then applies. | Split or chunk the value, or keep it in an external store and keep a reference. |
| Pending messages in one Channel | 1000 | A publication that would exceed it fails as a whole: a direct publish or RPC with RESOURCE_EXHAUSTED, a Step completion by failing the attempt without retry. Messages that a canceled Step returns never fail. | Split a longer waiting list across Channels. |
| Active Step executions in one Flow run, waiting ones included, the timeout handler excluded | 1000 | A start, RPC, or Step completion that raises the count above it fails the same way. A Step that completes and starts one next Step keeps the count. | Batch the work. Run a very large fan-out as SubFlows or top-level Flows, bounding each level, its coordinator and its pending Join reports included, to what one run allows. |
| Request IDs kept per run, shared by all its RPC names and other recorded requests | 1000 | Never rejects: the oldest IDs are forgotten, and an older request ID runs as a new request. | A long-lived Flow that must reject a late repeat checks a business key in its own state. |
| Concurrent Client waits on one Flow ID, across its runs: Flow, Step-completion, and Attribute-match waits | 1000, and 100,000 per Server node | A wait beyond either fails with RESOURCE_EXHAUSTED. | Let one reader wait and share the result, or read on demand. |
| Results a child Flow hands to its parent | 16 MiB in total, blob-backed outputs counted at their full size | Each completing decision of the child that carries an output and would exceed it fails the attempt without retry; unless its failure policy recovers, the child fails and the parent receives that failure. | Store the data outside the result and return a small application-owned reference. |

## Must

This is the only hard design rule. Everything else in this skill is a fact, a
pattern, or a suggestion.

**Make each external effect safe to repeat.** Every Step method and timeout handler may run more than once, and an old attempt can still be calling a provider when its retry starts. An RPC handler runs again only when its caller calls again, which an unknown outcome, such as a timeout, often forces. Key the effect by an ID that stays the same across these runs and that the receiver deduplicates, or make it compensatable.

Facts: [external calls are outside the commit](#commits-and-isolation), [every Step method may run more than once](#repeated-execution-snapshots-and-locks), [RPCs](#rpcs), and which IDs stay the same in [IDs across repeats](#ids-across-repeats).

External effects usually run in a Step's Execute, where retries and failure routes apply.

## Patterns

A pattern combines several facts for a common business shape: when the
business needs X, a shape that works is Y, because of the facts it names.
Patterns are suggestions; derive a variant from the same facts when the
business differs. A consequence of one or two facts is not a pattern: read
the facts.

Consult the matching pattern when several Dex guarantees interact in a design.

| Business shape | Pattern | Shape | Derived from |
| --- | --- | --- | --- |
| Wait for a person's or system's decision, with a deadline or escalation | [Decision with Deadline](references/core/patterns/control-structures.md#decision-with-deadline) | One waiting Step races the decision Channels and a Timer with AnyOf, and Execute routes on the Condition that fired. After the deciding Execute commits, a later delivery stays queued or is rejected according to the application's admission policy and the Flow's state. | [Conditions](#conditions-and-channel-messages), [Timers](#timers), [Commits](#commits-and-isolation), [Locks](#repeated-execution-snapshots-and-locks), [RPCs](#rpcs) |
| A caller must learn whether its reply made the deadline | [Close a reply window](references/core/patterns/control-structures.md#close-a-reply-window-with-an-attribute-lock) | Every deciding RPC and the deciding Execute share one Attribute lock and check and close the same window. When the Timer wins, that Execute checks under the lock for a reply admitted before it took the lock. The application computes the business cutoff in its own calendar and names which timestamp decides admission; for a hard cutoff, the reply RPC also compares the clock with a deadline the Flow stored. The caller maps only the Flow's closed or closing errors to "too late". | [Locks](#repeated-execution-snapshots-and-locks), [RPCs](#rpcs), [Conditions](#conditions-and-channel-messages), [Timers](#timers) |
| One event outranks another, such as a withdrawal over a decision | [Arbitrate by business priority](references/core/patterns/control-structures.md#arbitrate-by-business-priority) | The design keeps the priority rule and states whether "the same moment" means commit order or events admitted before the decision. | [Conditions](#conditions-and-channel-messages), [Locks](#repeated-execution-snapshots-and-locks) |
| Act after a period of inactivity, or take replies until a resolution arrives or the conversation goes idle | [Idle Timer](references/core/patterns/polling-and-timers.md#idle-timer), [Idle Timer with Resolution](references/core/patterns/polling-and-timers.md#idle-timer-with-resolution) | One Step waits with AnyOf on the activity or reply Channel, any resolution Channel, and an idle Timer; activity is recorded and loops back for a fresh Timer, and a fired Timer acts. | [Conditions](#conditions-and-channel-messages), [Timers](#timers) |
| Bursts of events per entity, each entity handled in order by one short Flow | [Drain an external Channel](references/core/patterns/channels-and-updates.md#drain-an-external-channel) | One Flow ID per entity under Allow if no running: RPCs publish to a Channel, one Step handles each message and closes with ForceCompleteIfChannelsEmpty, and the caller starts a new run when none is active. | [Flow completion](#flow-timeout-completion-and-cancellation), [ID reuse](#starting-a-flow-and-id-reuse), [RPCs](#rpcs) |
| A collection that can grow without a small business bound | [Sequentially chunked AttributeMap](references/core/patterns/flow-shapes-and-state.md#sequentially-chunked-attributemap) | Appends load only the current chunk, reads load one page, and a long-lived Flow deletes or archives old chunks. | [Default limits](#default-limits), [Loading](#attributes-loading-and-secrets), [Locks](#repeated-execution-snapshots-and-locks) |
| Keyed records with direct lookup | [Hash-partitioned AttributeMap](references/core/patterns/flow-shapes-and-state.md#hash-partitioned-attributemap) | A stable hash of the key picks one of a fixed number of map instances, and only that instance is locked and loaded. A hot counter can be split the same way; one Client read of every known shard of one run sums them at one committed snapshot, which shards across several Flows lose. A global invariant, such as a count that must not go negative, cannot be checked per shard. | [Default limits](#default-limits), [Loading](#attributes-loading-and-secrets) |
| Wait for an external system to become ready or finish | [Polling](references/core/patterns/polling-and-timers.md#polling) | A bounded WaitFor Timer loop with one provider call per Execute, or one long-running Execute with short calls and heartbeats. | [WaitFor and Execute](#waitfor-and-execute), [Workers and time bounds](#workers-attempts-and-time-bounds), [Snapshots](#repeated-execution-snapshots-and-locks), [Streams](#streams) |
| Work must happen at a deadline that spans retrying work, such as a notice or a withdrawal | [Deadline watchdog](references/core/patterns/polling-and-timers.md#deadline-watchdog) | The work runs as the only branch of a Join, and a join Step waits on its result or the deadline Timer, then decides alone. The watchdog reacts when the Timer fires, which can be late; a strict external cutoff is enforced at admission or at the receiver. | [Timers](#timers), [Time bounds](#workers-attempts-and-time-bounds), [Cancellation](#flow-timeout-completion-and-cancellation), [Joins](#joins) |
| Recurring work, or reminders until the recipient opts out | [Cron schedule](references/core/patterns/polling-and-timers.md#cron-schedule), [Reminder](references/core/patterns/polling-and-timers.md#reminder) | Cron is fixed-interval recurrence, not calendar recurrence: it starts the run and the next Timer wait together; a calendar schedule computes each next occurrence from a stored anchor. Reminder races the Timer with the opt-out, then rearms after sending. | [Timers](#timers), [Conditions](#conditions-and-channel-messages) |
| A failure that a person must resolve | [Manual recovery](references/core/patterns/failure-handling.md#manual-recovery) | The Execute failure route goes to a Step that waits on retry and skip Channels, and Execute routes on the one that fired. When a losing decision must not stay queued for the next failure, or a caller must learn whether its decision counted, the RPCs check and claim the failure under one lock. | [WaitFor and Execute](#waitfor-and-execute), [Conditions](#conditions-and-channel-messages), [Locks](#repeated-execution-snapshots-and-locks) |
| Undo earlier Steps after a Step fails | [Execute failure recovery](references/core/patterns/failure-handling.md#execute-failure-recovery) | The failure route compensates. When the failed Step made an external call, it undoes that Step first with its original reference, and every undo is a no-op for an action that never happened. A canceled or timed-out attempt can complete its write after compensation starts, so the Flow keeps the identity of every attempted effect, reconciles uncertain outcomes, and uses a withdrawal the provider applies against late writes before it declares the work undone. A WaitFor that runs out of retries fails the Flow by default; recovery that must undo earlier work routes that failure into Execute. | [WaitFor and Execute](#waitfor-and-execute), [Commits](#commits-and-isolation), [Workers](#workers-attempts-and-time-bounds) |
| Keep trying an unreliable call for up to a time, such as an hour | [Time bounds](references/core/step.md#time-bounds) | The retry total duration plus an Execute failure route. The last attempt can run past the limit, by about one method timeout when Workers are available and longer when retries queue. When work must happen at the deadline, add the deadline watchdog; a strict external cutoff also needs a check at the receiver. | [Time bounds](#workers-attempts-and-time-bounds), [WaitFor and Execute](#waitfor-and-execute) |
| A fixed deadline for the whole process | [Graceful timeout](references/core/patterns/failure-handling.md#graceful-timeout) | A deadline fixed at start for the whole Flow: the Flow timeout and its timeout handler. A deadline that moves or covers part of the Flow uses the deadline watchdog. | [Flow timeout](#flow-timeout-completion-and-cancellation) |
| Independent branches of one lifecycle, then a join | [Parallel Steps](references/core/patterns/parallel.md#parallel-steps) | GoToMany; a Join waits for all, the first, or n branches. Branches that need their own retries, failure routes, or durable results are Steps, not tasks inside one Execute. Active Steps and pending Join reports both count toward one run's limits, so a large fan-out is paged across bounded levels, and progress readers need is committed at that granularity. | [Joins](#joins), [Cancellation](#flow-timeout-completion-and-cancellation), [Snapshots](#repeated-execution-snapshots-and-locks) |
| Branches take turns on shared multi-Step work | [Critical section](references/core/patterns/control-structures.md#critical-section) | A handoff Step takes the only token, and every exit that leaves the Flow running returns it, including each Step's Execute failure route and early exits. | [Conditions](#conditions-and-channel-messages), [Cancellation](#flow-timeout-completion-and-cancellation), [Commits](#commits-and-isolation) |
| A multi-step mutation started from an API | [Own downstream starts durably](references/core/flow.md#own-downstream-starts-durably) | The API starts one domain-named Flow, and its Steps start the rest. Separate starts are separate commits, so a crash can leave some started; the owning Flow is the durable place that resumes the rest, retrying each logical start with its stable identity. Stopping the owner stops none of the work it already started, so the owner, or whoever stops it, reconciles accepted work. | [Starting a Flow](#starting-a-flow-and-id-reuse), [Workers](#workers-attempts-and-time-bounds) |
| Child jobs tracked by their own number: looked up, retried, stopped, or started by others | [Top-level child Flows](references/core/flow.md#child-jobs-with-their-own-identity) | A Step starts each child through the Client with a stable Flow ID and Request ID; the child reports through an RPC or the parent reconciles its terminal state. When anything else can start the child's Flow ID, the parent claims an already-started run only when its stored business identity matches. | [ID reuse](#starting-a-flow-and-id-reuse), [Runs](#runs-reads-and-outputs), [SubFlows and child Flows](#subflows-and-child-flows), [Flow timeout](#flow-timeout-completion-and-cancellation) |
| At most N jobs run at once per owner, with a bounded waiting list and a close that drains | [Long-lived parent](references/core/patterns/parallel.md#long-lived-parent), [Back pressure](references/core/patterns/parallel.md#back-pressure) | One long-lived Flow per owner runs N worker Steps that each take one request from a shared Channel. A worker's unlocked wait consumes a request; its Execute then takes the admission lock and decides whether to process or return that request. The admission and close RPCs take the same lock, and the design defines which queued requests a close must finish. | [Conditions](#conditions-and-channel-messages), [Default limits](#default-limits), [RPCs](#rpcs), [Locks](#repeated-execution-snapshots-and-locks) |
| Pause, stop, or resize running work through RPCs | [Control Long-Running Work](references/core/patterns/channels-and-updates.md#control-long-running-work) | RPCs write control Attributes that each new Execute of the loop reads; a control that must interrupt a wait also publishes to a Channel the wait includes. | [Starting a Flow](#starting-a-flow-and-id-reuse), [Snapshots](#repeated-execution-snapshots-and-locks), [WaitFor and Execute](#waitfor-and-execute) |
| Change Flow code while runs are open | [Versioning Flow code](references/core/application.md#versioning-flow-code) | Each run stores its behavior version at start, every Worker reads old and new versions, and old Waits keep the Conditions they committed. | [Workers](#workers-attempts-and-time-bounds), [WaitFor and Execute](#waitfor-and-execute) |
| Interrupt one turn of a conversation or job | [Stop a turn](references/core/patterns/ai-agents.md#stop-a-turn-with-canceling-steps) | A turn-scoped stop RPC commits the stop request, cancels the turn's Step, and starts a Step that reconciles from committed state; the provider call observes cancellation, and readers refresh the committed turn snapshot. | [RPCs](#rpcs), [Cancellation](#flow-timeout-completion-and-cancellation), [Streams](#streams) |

The [pattern index](references/core/patterns.md) lists every pattern, including
Flow boundaries, entity Flows, Workflow DSL, and durable AI agents.

### Suggestions

Usual choices that a design may leave when the business needs otherwise.

- **WaitFor sets up the wait.** Provider writes usually run in Execute: an Execute failure can route to another Step, and Dex Web shows WaitFor as the wait, so a write there is hard to see.
- **Short locked methods.** A lock that an RPC takes is usually held only by methods that make no external call and finish quickly: a failing or redeployed locked Step keeps the lock through its retries, and every RPC that needs it fails for that whole time.
- **Consistent lock declarations.** Keep per-call lock overrides consistent with the registration, and register the same Attribute and Channel definitions on every Client and Worker, because each checks writes only against what it registered.
- **Poll from Execute.** Usually poll with a WaitFor Timer and one provider call per Execute, or one long-running Execute with heartbeats; a read in Execute gets Execute's retries and failure route, and its result commits.
- **Race a Timer in one waiting Step.** Usually race a Timer with a Channel in one waiting Step, so Execute sees which fired, instead of a Timer-only Step that an RPC cancels.
- **One Step type per kind of work.** Dex Web's graph shows Step types, and a Step type supplies default options, so a domain-named Step type per kind of work, such as `ReserveStock`, shows the process and gets its own defaults. A movement can override options for one Step execution, such as a per-provider timeout, so options alone need no extra Step type.
- **Commit boundaries.** Give a provider action its own Step when its success deserves its own checkpoint, retry policy, failure route, or audit boundary.
- **The Flow owns decisions and status.** Pass business facts as start input or initial Attributes and let Steps compute decisions and status, so the rules live in one place and status changes commit with the Flow's progress. See [client.md](references/core/client.md#the-flow-owns-decisions-and-status).
- **Flow boundaries.** A call between Flows repeats when the calling method retries. A mutating call adds durable work and history to the target; a read-only call need not. Usually keep work whose parts would call each other on every operation, callback, or poll in one Flow; split anyway when the parts have different owners or lifecycles and the per-call cost is acceptable. See [flow.md](references/core/flow.md).
- **Parallel Steps before SubFlows.** Prefer parallel Steps inside one Flow, or a separate top-level Flow for a separate owner. Use a SubFlow when the child needs its own lifecycle, history, or retries, or when one fan-out is very large. See [subflow.md](references/core/subflow.md#when-to-use-a-subflow).
- **Dex-first state.** Keep new process state in Attributes and AttributeMaps. Use a store outside Dex when another system already owns the fact, or Dex's model does not fit an access pattern or scale need; name one owner per fact. See [data-handling.md](references/core/data-handling.md#dex-first-state-ownership).
- **Route confirmed permanent errors.** When a provider confirms a permanent error, route it from Execute to a failure-handling Step or ForceFail instead of returning it. A permanent WaitFor read error reaches Execute through a local value and a ready Wait, or through the WaitFor failure policy that proceeds to Execute; see [step.md](references/core/step.md#waitfor-versus-execute).
- **"Not ready" is a normal loop.** In a polling loop, use a normal GoTo for "not ready yet" instead of a retry; retries mix not-ready with real failures and spend the retry budget.
- **Cancellation reaches the call.** A call that does not observe the handler's cancellation keeps its Worker slot until it returns, even after Dex ends the attempt, so pass the cancellation signal into each call or give it its own timeout.

## Find the right reference version

This file states the design-relevant facts and defaults. Exact API names, signatures, option names, error classes, configuration keys, and version differences live in the SDK doc comments and the versioned Developer reference.

1. Inspect the existing manifests and source before you select tools. Find the installed SDK version in the project manifest or lockfile: `go.mod` (Go), `pyproject.toml` or the lockfile (Python), `pom.xml` or `build.gradle` (Java), `package.json` or the lockfile (TypeScript), `Cargo.toml` or `Cargo.lock` (Rust). Note its minor version.
2. Open the Developer reference for that component. `https://docs.superdurable.io/reference/<component>/` is the latest version; components are `go`, `python`, `java`, `typescript`, `rust`, `server`, `cli`, and `web`.
3. Select the project's version with the version selector in the left sidebar, then use the URL it opens. The [reference overview](https://docs.superdurable.io/reference/) has the compatibility matrix.
4. For the Server, the CLI, and Dex Web, use the version that is deployed or installed (`dexcli version`).
5. To read a docs page as text, follow its **View as Markdown** link when available, then select the Markdown variant for the application's language if the page lists one.

- An existing project keeps its pinned language toolchain, Dex SDK, `dexcli`, Server, Connector, and frontend versions unless the user asks for an upgrade.
- Treat the project's source, lockfile, installed SDK doc comments, and the matching runnable [examples](https://github.com/superdurable/dex/tree/main/examples/) as authoritative for exact code. Copy API shapes from runnable examples, not from memory. Examples track Dex `main` at the time this skills release was built; check a linked example against the installed SDK before copying its API calls.
- If you cannot verify an exact API for the installed version, stop at the version-independent Flow model and ask to inspect the installed package. Never invent a Dex API.

## Stay at the application boundary

- Model behavior with Flows, Steps, Waits, Attributes, Channels, Streams, RPCs, Timers, SubFlows, Connectors, Workers, and the Client.
- Outside code changes a running Flow through typed RPCs. Inside a Step or RPC, usually read and update its own Flow through the handler Context; see [Commits and isolation](#commits-and-isolation) for what a Client call into its own Flow does. From Execute, a Client usually serves another Flow, such as a top-level Flow or a SubFlow stopped by the ID its Context supplies. Read stored Attributes with the Client Attribute read, and use a read-only RPC when the read needs Flow code, pending messages, or a whole AttributeMap. Wait for a state change with an Attribute-match wait.
- Inspect and recover with Dex Web, dexcli, SDK errors, and application logs.
- Do not present Dex Server internals as application requirements. Discuss them only when the user develops or operates Dex itself.
- Diagnose read-only. Do not stop, time travel, publish, invoke, delete, edit, or otherwise mutate a Flow unless the user authorizes it.

## Load references

1. Read the language entry file for the project: [Go](references/go/go.md), [Python](references/python/python.md), [Java](references/java/java.md), [TypeScript](references/typescript/typescript.md), or [Rust](references/rust/rust.md). Load only that language.
2. When a Client call (start, RPC, wait, cleanup, admission, Stream write) can fail, repeat, or return an unknown outcome, read [error handling](references/core/error-handling.md) and the language's error handling file.
3. For a write followed by a read or a completion check, read [read-after-write](references/core/client.md#read-after-write).
4. When several Dex guarantees interact, check the runnable examples and the matching pattern. The [design review](references/core/self-check.md#design-review) is an optional aid.
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
| Server behavior, search backends, deployment | [server.md](references/core/server.md) |
| dexcli, `dexcli dev`, visualization | [cli.md](references/core/cli.md) |
| Dex Web | [web.md](references/core/web.md) |
| FDG annotations, Summary and Display, Start forms, Actions | [FDG authoring](references/fdg-authoring.md) |
| Design review and handoff | [self-check.md](references/core/self-check.md) |

## Complete the vertical slice

For a new Flow, register the definitions it uses with the Worker: Flow and Step definitions, every persistence schema entry, and the registry, Worker, and Client wiring. When Dex Web renders the Flow, the graph reads from source: return analyzer-visible decisions from Execute, use targets the analyzer can resolve, compose Steps instead of subclassing them, and fix every `dexcli visualize` error diagnostic before handoff.

Before handoff, you may review the code against the [code check](references/core/self-check.md#code-check); list in the handoff only the items it does not meet.

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
