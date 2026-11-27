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
| A Dex product, application, or business process, end to end | **Product route**: the [workflow](#the-workflow) stages, the [product constraints](#product-constraints), and every section from [Mental model](#mental-model) on |
| Learn, install, set up, or understand Dex, with no concrete process yet | [Getting started](references/getting-started.md) |
| Implement, debug, test, or operate code that uses the Dex SDK, such as fixing a Flow or adding a Step | **SDK-only route**: skip discovery, the UI and Connector questions, and the product constraints. Start at [Mental model](#mental-model) and keep the application's existing language, structure, and versions |
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

## The workflow

The product route builds the backend: the Flows, their Dex state, the Workers,
and the application API that the application's UI or callers use. It has one
reference per stage. Read only the sections the next decision needs. Record
decisions in the application's own documentation and do not reread a
reference once its facts are recorded.

| Stage | Goal | Reference |
| --- | --- | --- |
| 0. Workspace | Classify the repository, choose or keep the language, install tools | [Workspace bootstrap](references/workspace-bootstrap.md) |
| 1. Discovery | Actors, roles, permissions, lifecycle, Flow boundaries, storage, external systems; ask once about UI and Connectors | [Product discovery](references/product-discovery.md) |
| 2. Backend | Flow design, Dex-first state, Workers, the application API contract when a UI or caller needs one | [Backend implementation](references/backend-implementation.md) |
| 2a. FDG | Start input, Step groups, explanations, Summary, Display, Actions, diagnostics | [FDG authoring](references/fdg-authoring.md) |
| 3. Verify and hand off | Source gate, local `dexcli dev` stack, real journeys, packaging, handoff | [Verification and handoff](references/verification-handoff.md) |
| 3'. Managed Dex App | Repository rules, checks, import, and Live for Dex Cloud | [Managed Dex App](references/managed-dex-app.md) |

The UI and Connectors are optional. Discovery asks the user once about each.
Load these references only for the options the user chose:

| The user wants | Reference |
| --- | --- |
| Their own UI, which most products need | [UI workflow](references/ui-workflow.md) |
| Dex Web for operations and management, alone or with their own UI | [Dex Web](references/dex-web-v2.md) |
| A released Connector for an external system | [Connector architecture](references/connector-architecture.md) |

Before Stage 0 picks a language, ask whether the application runs locally only
or is imported into Dex Cloud as a Managed Dex App. A Managed Dex App starts
from the example repository's shape instead of Stage 0's bootstrap. Build it
to its repository rules, and hand
off with its checks and import steps instead of the local `dexcli dev` steps.
Import and publishing stay the user's steps.

Keep source readiness separate from configured real execution at every stage.
Never report compilation as provider or end-to-end success.

## Product constraints

Product route only; the SDK-only route skips this section. These rules add
to the general rules in [Model before implementation](#model-before-implementation)
and are stricter than them. They take precedence.

- **One backend language, chosen in Stage 0.** Go, Python, TypeScript, Java,
  and Rust are equal choices. Stage 0 keeps an existing application's
  language, uses the language the user names, and otherwise uses TypeScript.
  The rest of the workflow is the same for every language.
- **Strict FDG 2.0, with the directives listed for the language.** Use only
  the `dex:` directives and options that the
  [Flow visualization reference](https://docs.superdurable.io/reference/web/flow-visualization)
  lists for the application's language. Skip a capability it does not list.
  Never invent syntax, and never ask the user to switch languages for it.
  Give each local Step its `dex:group` and `dex:explanation` directives.
- **The UI is the user's choice.** Most products need their own UI. Dex Web
  can serve operations and management, alone or next to it. Never default to
  Dex Web alone, and never ask for a Dex Web gap before building the user's
  UI. When the user also uses Dex Web, leave its Runs, Work Queue, Actions,
  edits, history, and diagnostics to it unless the user asks for them in
  their own UI. Locally, `dexcli dev` always serves Dex Web to inspect Flows.
- **Dex is the default store.** Add a store outside Dex only when Dex's
  model does not fit an access pattern or scale need, and name one owner per
  fact. No speculative database, projection, or cache.
- **Flow boundaries.** Never split work whose parts
  would exchange RPCs or messages on frequent events, such as every
  operation, callback, or poll. Each call between Flows, even a read, adds
  latency and history to both Flows.
- **No mutation in WaitFor.** `WaitFor` mutates no provider and writes no Dex
  state except Step execution local values, which record a read error or pass
  a read value to `Execute`. It may
  make read-only, idempotent provider calls. Connector write
  operations run in `Execute`. Each provider effect in `Execute` has an
  idempotency identity and recovery.
- **Start identity.** Derive the Flow ID from the business operation. When
  a caller may retry a start after an unknown outcome, add a stable Request
  ID with ignore-already-started so the retry attaches to its own run. Set a
  reuse policy only when the default does not fit the lifecycle. Keep typed
  error recovery.
- **Write a short model.** For a non-trivial Flow, write a short model of
  its Steps, state, and failure handling in the application's own
  documentation before coding.
- **Credentials stay outside.** Keep them out of code, Flow state, logs, and
  definitions. The application reads them from its configuration or
  environment at run time. With a released Connector, it reads its
  connections through the project configuration loader of its language's
  Connector SDK, and locally Dex Web configures them.
  An operator or hosting platform supplies the deployed configuration. Dex
  hosts none.
- **Authorization at the trusted boundary.** A browser permission selection
  grants nothing. Enforce actor permissions at the trusted server boundary.
  Use stable, granular Action permissions.
- Do not mutate another repository or publish or deploy without
  authorization.

## Mental model

A **Flow** is one durable run of a business process, with a stable Flow ID. It is a graph of **Steps**. Each Step has **WaitFor**, which returns the Conditions to wait on, and **Execute**, which does the work and returns a decision: the next Steps, or how the Flow closes. Each method call is one commit boundary. Dex persists every committed change and retries failed methods, so the Flow survives Worker and Server restarts.

| Concept | What it is | Key point |
| --- | --- | --- |
| [Flow](references/core/flow.md) | durable execution with identity, schema, and RPCs | one Flow per business lifecycle, named after the domain |
| [Step](references/core/step.md) | WaitFor plus Execute; options set durability, timeouts, retries, loads, locks | one recovery boundary; its external effects are idempotent |
| [Attribute](references/core/attribute.md) | typed durable state; AttributeMap for keyed instances | the default home of application state |
| [Channel](references/core/channel.md) | durable FIFO queue consumed by WaitFor | queued intent, not storage |
| [RPC](references/core/rpc.md) | typed synchronous handler on a Flow | the only external write path for Flow state |
| [Stream](references/core/stream.md) | best-effort, resumable messages | progress only, never the record |
| [Timer](references/core/timer.md) | durable delay Condition | business delays, deadlines, and the interval of a Timer polling loop |
| [SubFlow](references/core/subflow.md) | child Flow started from WaitFor | for a child with its own lifecycle, history, or retries |
| [Client](references/core/client.md) | the application's boundary to Dex | start, invoke, read, wait; no direct state write |
| [Connector](references/core/connector.md) | released provider integration: ready-made Connector Steps (Query or Mutation) and Triggers | released official modules only; credentials never enter Flow state |

The Components are the processes that run this model: [application (Worker and Client)](references/core/application.md), [server](references/core/server.md), [dexcli and local development](references/core/cli.md), [Dex Web](references/core/web.md).

Facts the rules below rely on:

- Each WaitFor, Execute, and RPC call commits its Attribute writes and Channel publications together, only when it returns successfully. External calls are outside the commit, and Dex cannot roll them back.
- A commit is atomic but not isolated: two handlers that read and then write the same value lose an update unless both declare the same Attribute lock.
- Every method may run more than once. A retry reuses the first attempt's state snapshot and keeps its locks for the whole method call, including backoff.
- An RPC that needs a held lock fails at once with a lock conflict; it is not queued.
- When a wait is evaluated, Dex selects one ready Condition: a ready Timer first, otherwise the first ready Channel in declaration order. Otherwise the first event to commit wins. Only the winning Condition consumes messages.

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
| Separate owner and lifecycle | another top-level Flow, started by a Step through a Client |
| Wait for an external system | a Polling pattern: WaitFor Timer or long-running Step heartbeat |

### Default limits that shape a design

The Server rejects a request that would exceed a limit. These defaults decide a design; the current values are in the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits).

| Limit | Default | Design for it |
| --- | --- | --- |
| Active state of one Flow run | 64 MiB | Keep growing collections in a sequentially chunked AttributeMap, and delete or archive old chunks. |
| One value, such as an Attribute value or a Channel message | 16 MiB | Keep large payloads outside Dex and store a reference, or chunk them. |
| Pending messages in one Channel | 1000 | Split a longer waiting list across Channels. This does not depend on Server configuration. |
| Active Step executions in one Flow run, waiting ones included | 1000 | Bound the fan-out and batch large inputs. Split a very large fan-out across SubFlows or top-level Flows. |

## Find the right reference version

Skills hold stable rules only. Exact API names, signatures, options, defaults, limits, configuration, error codes, and version differences live in the SDK doc comments and the versioned Developer reference.

1. Inspect the existing manifests and source before you select tools. Find the installed SDK version in the project manifest or lockfile: `go.mod` (Go), `pyproject.toml` or the lockfile (Python), `pom.xml` or `build.gradle` (Java), `package.json` or the lockfile (TypeScript), `Cargo.toml` or `Cargo.lock` (Rust). Note its minor version.
2. Open the Developer reference for that component. `https://docs.superdurable.io/reference/<component>/` is the latest version; components are `go`, `python`, `java`, `typescript`, `rust`, `server`, `cli`, and `web`.
3. Select the project's version with the version selector in the left sidebar, then use the URL it opens. The [reference overview](https://docs.superdurable.io/reference/) has the compatibility matrix.
4. For the Server, the CLI, and Dex Web, use the version that is deployed or installed (`dexcli version`).
5. To read a docs page as text, follow its **View as Markdown** link when available, then select the Markdown variant for the application's language if the page lists one.

Rules:

- An existing project keeps its pinned language toolchain, Dex SDK, `dexcli`, Server, Connector, and frontend versions unless the user asks for an upgrade.
- Treat the project's source, lockfile, installed SDK doc comments, and the matching runnable [examples](https://github.com/superdurable/dex/tree/main/examples/) as authoritative for exact code. Copy API shapes from runnable examples, not from memory. Examples track Dex `main` at the time this skills release was built; check them against the installed version.
- If you cannot verify an exact API for the installed version, stop at the version-independent Flow model and ask to inspect the installed package. Never invent a Dex API.
- Discover filenames before you read module source. Batch independent read-only requests when the host supports it. Implement once the required types and lifecycle are understood. Let concrete compiler, FDG, or provider diagnostics drive further investigation.

## Stay at the application boundary

- Model behavior with Flows, Steps, Waits, Attributes, Channels, Streams, RPCs, Timers, SubFlows, Connectors, Workers, and the Client.
- Write Flow state only through typed RPCs. Inside a Step or RPC, read and update its own Flow through the handler Context, never through a Client. From Execute, use a Client only for another Flow, such as a top-level Flow or a SubFlow stopped by the ID its Context supplies. Read stored Attributes with the Client Attribute read, and use a read-only RPC when the read needs Flow code, pending messages, or a whole AttributeMap. Wait with Attribute match, not polling.
- Inspect and recover with Dex Web, dexcli, SDK errors, and application logs.
- Do not present Dex Server internals as application requirements. Discuss them only when the user develops or operates Dex itself.
- Diagnose read-only. Do not stop, time travel, publish, invoke, delete, edit, or otherwise mutate a Flow unless the user authorizes it.

## Load references

1. Read the language entry file for the project: [Go](references/go/go.md), [Python](references/python/python.md), [Java](references/java/java.md), [TypeScript](references/typescript/typescript.md), or [Rust](references/rust/rust.md). Load only that language.
2. Before you write any Client boundary (start, RPC, wait, cleanup, admission, Stream write), read [error handling](references/core/error-handling.md) and the language's error handling file.
3. For a write followed by a read or a completion check, read [read-after-write](references/core/client.md#read-after-write).
4. Then load what the task needs:

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

## Match the business shape to a pattern

Before you design a Flow, find its shape below and read the matching section. The section holds the pattern's rules, skeleton, and edge cases.

| Business shape | Pattern | Shape |
| --- | --- | --- |
| Wait for a person's or system's decision, with a deadline or escalation | [Decision with Deadline](references/core/patterns.md#decision-with-deadline) (Deferred choice) | One waiting Step races the decision Channels and a Timer with AnyOf, and Execute routes on the Condition that fired. |
| One event outranks another, such as a withdrawal over a decision | [Arbitrate by business priority](references/core/patterns.md#arbitrate-by-business-priority) | The design keeps the priority rule and defines "the same moment": commit order under a reply window, Channels ready together ranked by declaration order, or a locked round that ranks every admitted event. |
| Take replies until a resolution arrives or the conversation goes idle | [Idle Timer with Resolution](references/core/patterns.md#idle-timer-with-resolution) (Inbound event loop) | One Step waits on replies, the resolution, and an idle Timer, records the replies, and loops back for a fresh Timer. |
| Bursts of events per entity, each entity handled in order by one short Flow | [Drain an external Channel](references/core/patterns.md#drain-an-external-channel) | One Flow ID per entity: RPCs publish to a Channel, one Step handles each message and closes with ForceCompleteIfChannelsEmpty, and the caller starts a new run when none is active. |
| Close or act after a period of inactivity | [Idle Timer](references/core/patterns.md#idle-timer) (Inactivity tracker) | AnyOf(Timer(window), activity Channel); activity loops back for a fresh Timer, and a fired Timer acts. |
| A collection that grows without bound | [Sequentially chunked AttributeMap](references/core/patterns.md#sequentially-chunked-attributemap) | Appends load only the current chunk, reads load one page, and a long-lived Flow deletes or archives old chunks. |
| Keyed records with direct lookup | [Hash-partitioned AttributeMap](references/core/patterns.md#hash-partitioned-attributemap) | A stable hash of the key picks one of a fixed number of map instances, and only that instance is locked and loaded. |
| Wait until a deadline that a provider supplies, such as a due date | [Timer](references/core/timer.md) | A read-only, idempotent call reads the deadline in WaitFor, which returns the Timer. |
| Wait for an external system to become ready or finish | [Polling](references/core/patterns.md#polling) | A bounded WaitFor Timer loop with one provider call per Execute, or one long-running Execute with short calls and heartbeats. |
| Recurring work, or reminders until the recipient opts out | [Cron schedule](references/core/patterns.md#cron-schedule), [Reminder](references/core/patterns.md#reminder) | Cron starts the run and the next Timer wait together; Reminder races the Timer with the opt-out, then rearms after sending. |
| A failure that a person must resolve | [Manual recovery](references/core/patterns.md#manual-recovery) | The Execute failure route goes to a Step that waits on retry and skip Channels. |
| Undo earlier Steps after a Step fails | [Execute failure recovery](references/core/patterns.md#execute-failure-recovery) | The failure route compensates, starting with the failed Step itself. |
| Keep trying an unreliable call for up to a time, such as an hour | [Time bounds](references/core/step.md#time-bounds) | The retry total duration plus an Execute failure route to a Step that handles the failure; the duration counts from the first attempt, so the Step computes no deadline or budget guard of its own. |
| A deadline for the whole process | [Graceful timeout](references/core/patterns.md#graceful-timeout) | The Flow timeout and its timeout handler, not a competing Step. |
| Independent branches of one lifecycle, then a join | [Parallel Steps](references/core/patterns.md#parallel-steps) | GoToMany; a Join waits for all, the first, or n branches. |
| Branches take turns on shared multi-Step work | [Critical section](references/core/patterns.md#critical-section) | A handoff Step takes the only token, and every exit of the section returns it. |
| Child jobs tracked by their own number: looked up, retried, stopped, or started by others | [Top-level child Flows](references/core/flow.md#child-jobs-with-their-own-identity) | A Step starts each child through the Client with a stable Flow ID and Request ID, and the child reports through an RPC or the parent reconciles its terminal state. |
| At most N jobs run at once per owner, with a bounded waiting list and a close that drains | [Long-lived parent](references/core/patterns.md#long-lived-parent), [Back pressure](references/core/patterns.md#back-pressure) | One long-lived Flow per owner runs N worker Steps that each take one request, and admission, close, and each take share one lock on the close flag. |
| Pause, stop, or resize running work through RPCs | [Control Long-Running Work](references/core/patterns.md#control-long-running-work) | RPCs write control Attributes that each iteration reads, applying a default for an unset control at read time. |

## Model before implementation

For non-trivial work, check the runnable examples and [patterns](references/core/patterns.md), then write a short Flow model before code. The [design review](references/core/self-check.md#design-review) is an optional aid. These rules apply on every route. Each section's heading states its strength: Must rules prevent lost data, duplicate effects, deadlocks, lost wake-ups, or wrong business results; Fine lists what needs no extra guard; Prefer gives defaults that a design may leave.

### Must

- **Deadlines that trigger work are durable Timers.** A deadline that must trigger work is a Timer Condition, so the Flow wakes when it passes; a deadline for the whole Flow is the Flow timeout. Never use a Timer-only Step that an RPC cancels. When a deadline spans retrying work, such as a polling loop's rounds, use the [deadline watchdog](references/core/patterns.md#deadline-watchdog).
- **No identity from the clock.** Never derive an idempotency key or business ID from the current time; each retry reads a different time. Use the Flow ID and the Flow start time from the Context.
- **Check which Condition fired.** When a decision has a deadline, race its decision Channels and Timer in one waiting Step. In Execute, check which Condition fired; never infer the Timer from "no Channel result", an open window, or an unset outcome.
- **Close a reply window when "no reply before the deadline" must hold.** A reply can commit after the Timer won and before Execute runs, and its RPC returns success. Close a [reply window with an Attribute lock](references/core/patterns.md#close-a-reply-window-with-an-attribute-lock) that the reply RPCs and the deciding Execute share; that Execute still takes a reply that committed before it closed the window. The reply RPC may record the admitted reply as the outcome under that lock.
- **Keep business priority rules between events.** Keep a rule such as "a confirmation wins over a decline at the same moment"; never drop it as unenforceable. State which meaning of "the same moment" the design uses:
  - (a) Commit order: a reply window admits one reply at a time, and the first committed reply wins. This is commit order, never presented as a business rank.
  - (b) Wait evaluation: among Channels ready together, AnyOf declaration order ranks them. This ranks Channels only; a ready Timer is selected first.
  - To rank every event admitted before the decision, use the locked round of [Arbitrate by business priority](references/core/patterns.md#arbitrate-by-business-priority).
- **No grace window after the decision.** Once the decision's snapshot or the closed window fixes the candidates, a later event changes nothing; never wait for later events with a clock or Timer grace window. A next Step that takes a reply admitted before that boundary is not such a wait.
- **No provider writes or polling in WaitFor.** WaitFor may run more than once, so it never mutates an external system, including through Connector write operations, and never polls a provider; waiting for an external system uses a [Polling](references/core/patterns.md#polling) shape. Every provider write runs in Execute, idempotent or compensatable, keyed by an ID that is stable across attempts.
- **No external call under a lock that an RPC takes.** Make no external call, including starting or calling another Flow, in a WaitFor, Execute, or timeout handler that holds a lock an RPC also takes. Dex holds the lock for the whole method call, through retries and backoff, so the RPC fails with a lock conflict meanwhile. Decide and commit under the lock, then make the call in the next, unlocked Step.
- **Stop an already-decided action by the business requirement.** When an RPC stops an action that a Step decides, such as a reminder, choose by what the business promises; parallel waiters and the per-branch grace follow the [Reminder](references/core/patterns.md#reminder) rules.

  | The business accepts | Design |
  | --- | --- |
  | The decided action may still happen once after the stop | The stop RPC takes no lock, publishes the stop, and answers "received". The deciding Execute checks the stop Channel, also when a Timer won, and every later interruptible wait includes the stop. The action carries a stable occurrence ID that the receiver deduplicates. |
  | No action may be decided after the stop RPC returns | The stop RPC and the deciding Step share a lock. The deciding Step commits the decision under the lock only when no stop is committed, and the next, unlocked Step acts. |

- **Concurrent writers publish, they do not lock shared state.** Route frequent concurrent writers through an RPC that publishes to a Channel consumed by one writing Step. Never have each RPC lock and rewrite shared state, such as a list, a counter, or an admission deadline; a lock conflict refuses the caller even when it retries.
- **One active consumer per Channel at a time.** At any moment, at most one waiting Step consumes a given Channel, so which Step receives a message never depends on timing. For an event every waiter must receive, use one ChannelMap key per waiter and publish to each key.
- **Bound growing collections.** Keep them in a [sequentially chunked AttributeMap](references/core/patterns.md#sequentially-chunked-attributemap), never in one Attribute rewritten on each change; otherwise the value and the Flow's active state keep growing toward their [limits](#default-limits-that-shape-a-design). A long-lived Flow also deletes or archives old chunks.
- **Branches with their own effects are Steps.** When branches make their own external effects or need their own retries, failure routes, or durable results, run them as parallel Steps: GoToMany with a sibling cancel, or a Join for the first k of n. See [First-win parallel Steps](references/core/patterns.md#first-win-parallel-steps).
- **Silent calls need a higher heartbeat timeout.** A call that can stay silent longer than the heartbeat timeout, which is short by default, gets a higher heartbeat timeout or heartbeats; otherwise every attempt times out and the Step fails when retries run out. Raising only the method timeout keeps the heartbeat deadline. Pass the cancellation signal into each call that accepts one; see [Time bounds](references/core/step.md#time-bounds).
- **Compensation starts with the failed Step.** A Step whose retries ran out may still have taken effect, for example when the provider applied the change and then timed out. Undo it first with its original reference, and make every undo a no-op for an action that never happened. See [Execute failure recovery](references/core/patterns.md#execute-failure-recovery).
- **Start first.** Call StartFlow first with a stable Flow ID and a Request ID derived from the complete logical start request; never read before or after a successful start to guard duplicates. To attach matching retries and reject changed details, use an ID reuse policy that rejects reuse with ignore-already-started, which applies only to a matching Request ID. See [error-handling.md](references/core/error-handling.md#start-first-reconcile-only-after-an-error).
- **A reuse policy reads only how the latest run closed.** It decides by the latest run's closed state, not by what the run saved. Allow-if-previous-failed admits a new run after any abnormal close, even one that already saved its record, and never after a run that recorded a business failure and completed.
- **One owning Flow.** Start one domain-named Flow at the API boundary for a multi-step mutation. Never chain dependent starts in the API handler; a crash between them leaves half the work started. See [flow.md](references/core/flow.md#own-downstream-starts-durably).
- **Child jobs with their own identity are top-level Flows.** SubFlow IDs derive from the parent. A child that people look up, retry, reopen, or stop by its own number is a top-level Flow whose business number is its Flow ID, started by a Step with a stable Request ID. The parent receives its result durably and accepts it idempotently; reopen and run-ID rules are in [flow.md](references/core/flow.md#child-jobs-with-their-own-identity).
- **Always catch a child's already-started error.** Under a rejecting reuse policy with ignore-already-started, it means another logical request owns that Flow ID, even one from the same entry point or parent run. Claim that run only when its stored business identity matches the request; otherwise record a rejected admission in its own state, never ended or failed, and never wait for, count, or stop that run.
- **A child may end without reporting.** StopFlow, a failure after a Step exhausts its retries, or the child's Flow timeout can end it before it reports. Let its recovery Step or timeout handler report, and when the parent must know the result, keep a reconcile Timer that reads the child's terminal state. Each starter sets the child's Flow timeout; the child never enforces its own limit with a Timer Step.
- **Durable state lives in Dex.** Never keep state that a later Step or RPC reads in Worker memory or on one Worker's disk; the next handler may run on another Worker.
- **Terminal reads.** Read retained state with a Client Attribute read or a query-only RPC; never decode Step outputs from history for a business read. Every result or status a user must see has such a read path: a result Attribute, or the Flow's completion output from WaitForFlow.
- **Typed failures.** Keep typed Dex errors until domain policy can tell a business rejection, a closed-Flow race, a retryable failure, and a local defect apart.
- **Open Flows.** Keep stable names compatible with open Flow runs; a renamed Step type or schema entry breaks runs that are still open. See [application.md](references/core/application.md#versioning-flow-code).
- **The graph reads from source.** On the product route, or whenever Dex Web renders the Flow, return analyzer-visible decisions from Execute, use targets the analyzer can resolve, compose Steps instead of subclassing them, and fix every `dexcli visualize` error diagnostic before handoff.

### Fine: no extra guard needed

- Comparing the clock with a stored deadline to validate a request, such as rejecting a late reply, is fine.
- Reading the time to record a timestamp or compute a schedule is fine: the committed value is the one that counts.
- "Keep trying for up to X" needs no deadline or budget check in the Step: the retry total duration counts from the first attempt, and the Execute failure route runs when it runs out.
- A Timer fires at or shortly after its time. The business needs no drift correction or second clock check.
- Steps need no deadline check of their own for the Flow timeout: a timeout handler that commits ForceComplete or ForceFail cancels them, and the fail or cancel timeout policy starts closing the Flow when the deadline fires.
- A read-only, idempotent call in WaitFor, such as reading a due date for a Timer, is fine.
- A lock that only Steps share may cover an external call; those Steps run one at a time.
- Synchronous read-modify-write by a few writers may share one Attribute lock across them.
- In-process concurrency inside one Execute, such as `Promise.all`, asyncio tasks, or threads, is fine for idempotent reads that succeed or retry together.
- Different Step types may consume the same Channel in different phases of the Flow.
- Short, idempotent methods need no StepOptions; the defaults fit them.
- A child number that only ever has one run needs no run-ID tie; the Flow ID is enough.
- Dex retries, heartbeat timeouts, and durability are Dex behavior; tests do not re-test them with shortened durations.

### Prefer

- **Names.** Prefer domain names for Flow and Step types, such as `ReserveStock`, over `Init`, `Start`, or `Process`, and verb phrases for RPC names, such as `getQueuedMessages`. Prefer one Step type per kind of work, so the graph shows it and it can have its own options. Flow types must be unique in a registry.
- **Commit boundaries.** Give a provider action its own Step when its success deserves its own checkpoint, retry policy, failure route, or audit boundary.
- **Prefer that the Flow owns decisions and status.** Pass business facts as start input or initial Attributes and let Steps compute decisions and status. Then the rules live in one place, and status changes commit together with the Flow's progress. A caller that precomputes a decision, such as "needs a second approval", spreads the rule across two places. See [client.md](references/core/client.md#the-flow-owns-decisions-and-status).
- **Prefer parallel Steps to SubFlows.** Prefer parallel Steps inside one Flow, or a separate top-level Flow for a separate owner. Use a SubFlow when the child needs its own lifecycle, history, or retries, or when one fan-out is very large. See [subflow.md](references/core/subflow.md#when-to-use-a-subflow).
- **Dex-first state.** Default durable state to Attributes and AttributeMaps, not a new database. See [data-handling.md](references/core/data-handling.md#dex-first-state-ownership).
- **Route confirmed permanent errors.** When a provider confirms a permanent error, prefer routing it from Execute to a failure-handling Step or ForceFail; a returned error is retried until the retry budget runs out. Errors that can succeed on a later attempt use Step retry. A permanent WaitFor read error is routed the same way; see [step.md](references/core/step.md#waitfor-versus-execute).
- **"Not ready" is a normal loop.** In a polling loop, prefer a normal GoTo for "not ready yet" to a retry; retries mix not-ready with real failures and spend the retry budget.
- **Own timeouts for calls that ignore cancellation.** Prefer giving a call that cannot observe cancellation its own client timeout, such as a blocking call in a Rust handler or a Go call without the context; otherwise it keeps its Worker slot until it returns.
- **Provider-supplied deadlines.** Read them in WaitFor and return the Timer. Read them instead in the previous Step's Execute and pass them as the next Step's input when the value must be auditable or visible, or needs its own failure route.
- **Declare decision Channels in business priority order** in AnyOf.
- **Real durations in tests.** Prefer keeping the business duration and skipping the Timer through the Client. A configurable duration is fine when the business also needs it configurable. See [testing.md](references/core/testing.md#scenarios-to-cover).

## Complete the vertical slice

A change includes typed inputs, outputs, and failures; Flow and Step definitions; every persistence schema entry; constructor-injected dependencies; registry, Worker, and Client wiring; and an application boundary. When the project has tests or the user asks for them, test durable behavior against a real Dex Server with unique Flow IDs and bounded waits instead of sleeps.

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
- [Server](https://docs.superdurable.io/reference/server/)
- The runnable examples for the application's language:
  [Go](https://github.com/superdurable/dex/tree/main/examples/go),
  [Python](https://github.com/superdurable/dex/tree/main/examples/python),
  [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript),
  [Java](https://github.com/superdurable/dex/tree/main/examples/java), or
  [Rust](https://github.com/superdurable/dex/tree/main/examples/rust)
- [Connector catalog](https://connectors.superdurable.io/catalog.yaml) and
  [Connector library](https://github.com/superdurable/dex-connectors-library)
