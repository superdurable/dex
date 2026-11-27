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

These product-route rules add to the general rules in
[Model before implementation](#model-before-implementation) and are stricter
than them. They take precedence.

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

| Concept | What it is | Key rule |
| --- | --- | --- |
| [Flow](references/core/flow.md) | durable execution with identity, schema, and RPCs | one Flow per business lifecycle; name it after the domain |
| [Step](references/core/step.md) | WaitFor plus Execute; options set durability, timeouts, retries, loads, locks | split Steps by recovery boundary; external effects idempotent |
| [Attribute](references/core/attribute.md) | typed durable state; AttributeMap for keyed instances | the default home of application state |
| [Channel](references/core/channel.md) | durable FIFO queue consumed by WaitFor | queued intent, not storage |
| [RPC](references/core/rpc.md) | typed synchronous handler on a Flow | the only external write path for Flow state |
| [Stream](references/core/stream.md) | best-effort, resumable messages | progress only; never the record |
| [Timer](references/core/timer.md) | durable delay Condition | business delays, deadlines, and the interval of a Timer polling loop |
| [SubFlow](references/core/subflow.md) | child Flow started from WaitFor | prefer parallel Steps; use it when the child needs its own lifecycle, history, or retries |
| [Client](references/core/client.md) | the application's boundary to Dex | start, invoke, read, wait; no direct state write |
| [Connector](references/core/connector.md) | released provider integration: ready-made Connector Steps (Query or Mutation) and Triggers | use released official modules only; credentials never enter Flow state |

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
| Separate owner and lifecycle | another top-level Flow, started by a Step through a Client |
| Wait for an external system | a Polling pattern: WaitFor Timer or long-running Step heartbeat |

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

Before you design a Flow, find its shape below and read the matching section of [patterns.md](references/core/patterns.md). Each gist is the rule; the section adds the skeleton and edge cases.

| Business shape | Pattern | Gist |
| --- | --- | --- |
| Wait for a person's or system's decision, with a deadline or escalation | [Decision with Deadline](references/core/patterns.md#decision-with-deadline) (Deferred choice) | One waiting Step races the decision Channels and a Timer with AnyOf. Execute checks which Condition fired, then routes. RPCs only publish. |
| One event outranks another, such as a withdrawal over a decision | [Arbitrate by business priority](references/core/patterns.md#arbitrate-by-business-priority) | Keep the priority rule and state what "the same moment" means: the first committed reply under a reply window, or the Channels ready together when the wait is evaluated, ranked by declaration order. A Timer-won Execute that re-checks Channels selects what is committed in its first state snapshot; retries reuse that snapshot. To rank every event admitted before the decision, each RPC names its round and appends its candidate to AttributeMaps keyed by that round ID under the round's instance locks, and answers "received"; the arbitration Step closes the round under the same locks and picks the winner. |
| Take replies until a resolution arrives or the conversation goes idle | [Idle Timer with Resolution](references/core/patterns.md#idle-timer-with-resolution) (Inbound event loop) | RPCs validate and publish. One Step waits on replies, the resolution, and the idle Timer, and routes on the winning Condition: replies are recorded and the Step loops back for a fresh Timer. |
| Bursts of events per entity, each entity handled in order by one short Flow | [Drain an external Channel](references/core/patterns.md#drain-an-external-channel) | One stable Flow ID per entity, one active run at a time, messages handled in commit order. The RPC validates and publishes to a Channel; on a missing or inactive run the caller starts one, allowing reuse of a closed Flow ID. One Step handles each message and closes with ForceCompleteIfChannelsEmpty. No external queue. |
| Close or act after a period of inactivity | [Idle Timer](references/core/patterns.md#idle-timer) (Inactivity tracker) | AnyOf(Timer(window), activity Channel). Activity loops back for a fresh Timer; a fired Timer acts. |
| A collection that grows without bound | [Sequentially chunked AttributeMap](references/core/patterns.md#sequentially-chunked-attributemap) | Archive bounded chunks. An append loads only the current chunk and a read only the requested page, never the whole map. For a long-lived Flow, also bound the total by deleting or archiving old chunks, and record the oldest retained token in the same commit. |
| Keyed records with direct lookup | [Hash-partitioned AttributeMap](references/core/patterns.md#hash-partitioned-attributemap) | A stable hash of the key picks one of a fixed number of map instances; lock and load only that instance. Define the partition count as one named constant marked as persisted layout. |
| Wait until a deadline that a provider supplies, such as a due date | [Timer](references/core/timer.md) | Read it in WaitFor with a read-only, idempotent call and return the Timer; the Timer is durable once WaitFor's result commits. Read it instead in the previous Step's Execute and pass it as the next Step's input when the value must be auditable or visible, or needs its own failure route. |
| Wait for an external system to become ready or finish | [Polling](references/core/patterns.md#polling) | Either a bounded WaitFor Timer loop with one short provider call per Execute, which records each round in the history and is somewhat heavier, or one long-running Execute with short calls and heartbeats, which is lighter. Never poll the provider in WaitFor. Prefer a normal loop to retries for "not ready": retries mix not-ready with real failures and spend the retry budget. |
| Recurring work, or reminders until the recipient opts out | [Cron schedule](references/core/patterns.md#cron-schedule), [Reminder](references/core/patterns.md#reminder) | Cron: start the run and the next Timer wait together. Reminder: race the Timer with opt-out, then rearm after sending. |
| A failure that a person must resolve | [Manual recovery](references/core/patterns.md#manual-recovery) | The Execute failure route goes to a Step that waits on retry and skip Channels. |
| Undo earlier Steps after a Step fails | [Execute failure recovery](references/core/patterns.md#execute-failure-recovery) | The failure route compensates, starting with the failed Step itself, because a Step whose retries ran out may still have taken effect. |
| A deadline for the whole process | [Graceful timeout](references/core/patterns.md#graceful-timeout) | The Flow timeout and its timeout handler, not a competing Step. When the deadline must record an outcome, the handler records it and closes with ForceComplete or ForceFail; GracefulComplete waits for every other active Step. When it must also report to another system and holds a lock an RPC takes, it records the outcome and moves to an unlocked Step that reports idempotently, then force-closes; keep that handler SYNC so the outcome is stored before the report. |
| Independent branches of one lifecycle, then a join | [Parallel Steps](references/core/patterns.md#parallel-steps) | GoToMany; a Join waits for all, the first, or n branches. |
| Branches take turns on shared multi-Step work | [Critical section](references/core/patterns.md#critical-section) | A handoff Step consumes the only token. Every application-controlled exit returns it, including failure and early exit. Protected Steps finish through a token-releasing path. |
| Child jobs tracked by their own number: looked up, retried, stopped, or started by others | [Top-level child Flows](references/core/flow.md#child-jobs-with-their-own-identity) | A Step starts each child through the Client with a stable Flow ID and Request ID. An already-started error may be another logical request's child: claim it only on a matching stored business identity, otherwise record it in its own rejected state, never ended or failed. The child reports back through an RPC, or the parent polls its terminal state. When the parent's result depends on how a stopped or late child ended, it reconciles that child; otherwise it stops the child and moves on. |
| At most N jobs run at once per owner, with a bounded waiting list and a close that drains | [Long-lived parent](references/core/patterns.md#long-lived-parent), [Back pressure](references/core/patterns.md#back-pressure) | One long-lived Flow per owner runs N worker Steps that each take one request with ForOne. The admission RPC, the close RPC, and each worker's take commit share one lock on the close flag. Admission checks the flag and the Channel size and refuses when full; keep no separate waiting counter. Under that lock, each worker commits either the transition to the Step that starts the child, or the transition to a Step that returns the request. A child already admitted can finish after intake closes. The admission and close RPCs take that lock, so calls to other systems or Flows run in the next, unlocked Step. Idle workers end through the graceful completion Condition. |
| Pause, stop, or resize running work through RPCs | [Control Long-Running Work](references/core/patterns.md#control-long-running-work) | RPCs validate and write control Attributes; each iteration reads them. Apply a default for an unset control at read time, never in the start Step, which could overwrite an RPC that arrived first. |

## Rules agents most often miss

- **Deadlines that trigger work are durable Timers.** A deadline that must trigger work is a durable Timer, so the Flow wakes when it passes; it is also visible in the graph and skippable in tests. Comparing the clock with a stored deadline to validate a request, such as rejecting a late reply, is fine. Idempotency keys and business IDs never derive from the current time, which differs per retry; use the Flow ID and the Flow start time from the Context. A deadline measured from an event is a Timer started when that event is handled: after each activity, the next WaitFor starts a full Timer(window); for a deadline across a polling loop's rounds, the handling decision schedules the polling Step and a watchdog Step that waits on Timer(deadline). The provider call stays in the unlocked polling Step; a short result Step and the watchdog share a lock, each writes the terminal state only while it is open; the winner cancels the other by Step type and completes or goes to the close branch, and the loser returns DeadEnd. Never re-derive the remaining time from a stored timestamp: a fresh Timer is less code and skippable in tests, and the watchdog fires even while a round is retrying. See [Polling with WaitFor Timer](references/core/patterns.md#polling-with-waitfor-timer). Reading the time to record a timestamp or compute a schedule is fine: the committed value is the one that counts.
- **Deadlines race in one waiting Step.** When a decision has a deadline, race its decision Channels and Timer in one waiting Step. In Execute, check which Condition fired; never infer "deadline passed" from "no Channel result". When the wait is evaluated, Dex selects one ready Condition: a ready Timer first, otherwise the first ready Channel in declaration order. When Conditions become ready at different times, the first event to satisfy the wait wins. A decision can still commit after the Timer won and before Execute runs, and its RPC returns success. When the business must decide "no reply before the deadline", close the reply window with an Attribute lock: see [Close a reply window with an Attribute lock](references/core/patterns.md#close-a-reply-window-with-an-attribute-lock). Declare decision Channels in business priority order. A deadline for the whole Flow uses the Flow timeout. Never use a Timer-only Step that an RPC cancels.
- **Ranked events keep their business priority rule.** Keep a rule such as "a confirmation wins over a decline that arrives at the same moment"; never drop it as unenforceable. State in the design which definition of "the same moment" the code uses. Both are valid. (a) Server commit order: a reply window or lock admits replies one commit at a time, the first committed reply wins, and ties never occur. The reply RPC publishes the reply and changes only the window flag, in one locked commit; the waiting Step's Execute decides the outcome and records it, or moves to the branch that records it. (b) The competing Channels ready when the wait is evaluated: AnyOf declaration order ranks them strictly, and Execute uses the messages the winning Condition consumed. This ranks Channels only; a ready Timer is selected first. When a Timer wins and Execute re-checks the Channels, such as by their size, it selects the events committed in Execute's first state snapshot, which retries reuse and which is a later moment than the wait evaluation; say so when the design relies on it. Once the snapshot the decision uses or the closed window fixes the candidates, a later event changes nothing: never wait for new events with a clock or Timer grace window. A next Step that takes a reply admitted before that boundary is not such a wait. Separately, to rank every event admitted before the decision, use a locked round whose closed window defines eligibility; a reply window may be combined with ranked replies this way. Keep each round's window and candidates in AttributeMaps keyed by a round ID that is never reused. Each RPC names its round and, holding that round's instance locks, accepts only while that round is open, appends its candidate with its rank and admission order, and answers "received". The arbitration Step wakes on a Timer or a wake-up Channel; its Execute takes the same locks, closes the round, and picks the highest rank from its candidates, with admission order breaking a tie. Never present an RPC that decides by lock order as business priority: lock order is commit order, which is definition (a). See [Arbitrate by business priority](references/core/patterns.md#arbitrate-by-business-priority).
- **Prefer real durations in tests.** Prefer keeping the business duration and skipping the Timer through the Client. A configurable duration is fine when the business also needs it configurable. A fake permanent rejection under the production configuration verifies that branch and that it does not retry; in product work, requested tests trigger it through the real provider or record the branch as unverified, as in [verification-handoff.md](references/verification-handoff.md#tests-real-dependencies-or-none). A direct test verifies the recovery Step's logic. Dex scheduling the recovery Step after the real retry budget is not re-tested with shortened durations. See [testing.md](references/core/testing.md#scenarios-to-cover).
- **Prefer calls that can fail in Execute.** WaitFor returns the Conditions to wait on and never mutates an external system. It may make read-only, idempotent calls, such as reading a due date for a Timer; every provider mutation, including Connector write operations, runs in Execute. When a WaitFor read can fail permanently, let Execute route on it: record it in a Step execution local value and return a ready Wait, such as SkipWaitImmediately, or use the WaitFor failure policy that proceeds to Execute. When a provider confirms a permanent error, prefer routing it directly from Execute to a failure-handling Step or ForceFail; a returned error is retried until the budget runs out. Errors that can succeed on a later attempt use Step retry. See [step.md](references/core/step.md#waitfor-versus-execute) and the language's Step reference, such as [Go](https://docs.superdurable.io/reference/go/step#step-execution-local).
- **Branches with their own effects are Steps.** When branches make their own external effects or need their own retries, failure routes, or durable results, run them as parallel Steps: GoToMany with a sibling cancel, or a Join for the first k of n. In-process concurrency inside one Execute, such as `Promise.all`, asyncio tasks, or threads, is fine for idempotent reads that succeed or retry together. See [First-win parallel Steps](references/core/patterns.md#first-win-parallel-steps).
- **No external call under a lock that an RPC takes.** When an Execute declares a lock that an RPC also takes, do not call an external system in that Execute. When the business deadline for a stop is the decision commit, the stop RPC and the deciding Step share the lock; the deciding Step checks the committed stop under the lock, commits the decision only when no stop is committed, and the next, unlocked Step calls the external system. In general, decide and commit under the lock in that Step, then make the call in the next, unlocked Step. A lock that only Steps share may cover an external call; those Steps run one at a time. Dex holds a lock for the whole method call, through retries and backoff, and an RPC that needs it fails with a lock conflict meanwhile. The same applies to a WaitFor or a Flow timeout handler that declares such a lock. Another Flow counts as an external system, so starting or calling it through the Client is such a call. A polling Step never holds a lock that its deadline watchdog also needs; the watchdog would wait until the loop ends.
- **An accepted grace takes no lock.** When the business accepts that an already-decided action may still happen once, the stop RPC takes no lock, publishes the stop, and answers "received"; the deciding Execute checks the stop Channel in its first state snapshot, which retries reuse, also when a Timer was selected first; every later interruptible wait includes the stop Condition, and the Execute runs the action with a stable occurrence ID that the receiver deduplicates. The grace counts per decided action or branch; when the business allows only one such action overall, decide actions one at a time. Each parallel waiter gets its own stop message; a stop Attribute records the fact but wakes no waiter. See [attribute.md](references/core/attribute.md#locks).
- **Frequent concurrent appenders go through a Channel.** Route them through an RPC that publishes to a Channel consumed by one writing Step; never have each RPC lock and rewrite a shared list. Synchronous read-modify-write by a few writers uses the same Attribute lock across writers.
- **One active consumer per Channel at a time.** At any moment, at most one waiting Step consumes a given Channel, so which Step receives a message never depends on timing. Different Step types may consume the same Channel in different phases of the Flow. For an event every waiter must receive, use one ChannelMap key per waiter and publish to each key.
- **Bound growing collections.** Keep them in a sequentially chunked AttributeMap, never in one Attribute rewritten on each change. Each append loads only the current chunk, and each read loads only the requested page. For a long-lived Flow, also bound the total by deleting or archiving old chunks. Size limits are in the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits).
- **The graph reads from source.** On the product route, or whenever Dex Web renders the Flow, return analyzer-visible decisions from Execute, use targets the analyzer can resolve, compose Steps instead of subclassing them, and fix every `dexcli visualize` error diagnostic before handoff.
- **Silent calls need a higher heartbeat timeout.** Dex ends a hung attempt: the heartbeat timeout and the method timeout end it and send cancellation to the handler, and the retry policy retries it. Pass the cancellation signal, such as Go's context or TypeScript's abort signal, into each call that accepts one. Prefer giving a call that cannot observe cancellation its own timeout, set on its client or the underlying I/O, such as a blocking call in a Rust handler, a synchronous Python call, a Java call that ignores interrupts, or a Go call without the context: a stuck call keeps its Worker slot until it returns, while the method or heartbeat timeout still ends the attempt and Dex schedules later attempts by the retry policy. The default heartbeat timeout is short. If the attempt reaches its heartbeat deadline without a heartbeat or a Dex Stream write, Dex times out the attempt; raising only the method timeout keeps that deadline. A call that can stay silent longer than the heartbeat timeout needs a higher heartbeat timeout or heartbeats: set it above the longest interval without a heartbeat or Stream write, with a margin, including any polling sleep or other work, or heartbeat from a loop of short calls. A stand-in for an external service keeps the real call's heartbeat timeout and is never modeled as a Timer. Check the default retry total duration against the method timeout: set it when attempts are long enough that the default would allow fewer retries than you want, or when retries must stop sooner. After a failure, it decides whether Dex schedules another attempt. It does not cut short a running attempt, so a deadline for the whole Flow uses the Flow timeout, and a deadline for one wait uses a durable Timer Condition. The defaults fit short calls; see [step.md](references/core/step.md#time-bounds) and the language's StepOptions reference.
- **Compensation starts with the failed Step.** A Step whose retries ran out may still have taken effect, for example when the provider applied the change and then timed out. Compensation starts with that Step: undo it with its original reference, and make every undo a no-op for an action that never happened. See [Execute failure recovery](references/core/patterns.md#execute-failure-recovery).
- **Child jobs with their own identity are top-level Flows.** SubFlow IDs derive from the parent. A child that people look up, retry, or stop by its own number is a top-level Flow that a Step starts with a stable Flow ID and Request ID, and whose result the parent receives durably and accepts idempotently. Its business number is its Flow ID. A resend of the same start request keeps its Request ID. When people reopen a child by its own number, that reopen is a new start request with a new Request ID, under a reuse policy that allows the needed earlier closed state and rejects an active run. When the business can reopen a child under the same number while the parent still waits, and the parent must not mix runs, tie reads, reports, and stops to the claimed run ID, as [flow.md](references/core/flow.md#child-jobs-with-their-own-identity) details; when a child number has only one run, the Flow ID is enough.
- **Always catch a child's already-started error.** Under a rejecting reuse policy with ignore-already-started, it means another logical request owns that Flow ID, even one from the same entry point or parent run. Track that run only when its stored business identity matches the request; otherwise record a rejected admission in its own state, never ended or failed, list it as rejected in any report, and never wait for, count, or stop that run. Skip the identity check only when the mapping from business identity to child Flow ID guarantees that only a replay of the same logical request can start it again; people who retry or start the child by its own number break that guarantee.
- **A child may end without reporting.** The Flow timeout is a start option, so each starter sets it; the child never enforces its own limit with a Timer Step. StopFlow with cancel, fail, or terminate starts no new handlers in the child, so it may never report. A child that fails after a Step exhausts its retries, or ends by its Flow timeout, before it reports may never report either. Let its failure recovery Step or timeout handler report, and when the parent must know the result, keep a reconcile Timer that reads the child's terminal state and rearms while the child runs. When the parent's result or a follow-up action depends on how a stopped child ended, such as counting its result, undoing its side effects, or showing its outcome, the parent reconciles it from the child's stored state; children that finish after a quorum are handled the same way. When it does not, the parent stops the child and moves on. See [flow.md](references/core/flow.md#child-jobs-with-their-own-identity).

## Model before implementation

For non-trivial work, check the runnable examples and [patterns](references/core/patterns.md), then write a short Flow model before code. The [design review](references/core/self-check.md#design-review) is an optional aid. These rules apply everywhere:

- **Names.** Prefer domain names for Flow and Step types, such as `ReserveStock`, over `Init`, `Start`, or `Process`, and verb phrases for RPC names, such as `getQueuedMessages`. Prefer one Step type per kind of work, so the graph shows it and it can have its own options. Flow types must be unique in a registry.
- **WaitFor versus Execute.** WaitFor sets up a durable wait; any provider call there is read-only and idempotent. Provider writes run in Execute, as **Prefer calls that can fail in Execute** states. Keep every provider write idempotent or compensatable. Never poll a provider from WaitFor; use a [Polling pattern](references/core/patterns.md#polling).
- **Commit boundaries.** Each WaitFor, Execute, and RPC call commits separately. Give a provider action its own Step when its success deserves its own checkpoint, retry policy, failure route, or audit boundary.
- **Prefer that the Flow owns decisions and status.** Pass business facts as start input or initial Attributes and let Steps compute decisions and status. Then the rules live in one place, and status changes commit together with the Flow's progress. A caller that precomputes a decision, such as "needs a second approval", spreads the rule across two places. See [client.md](references/core/client.md#the-flow-owns-decisions-and-status).
- **One owning Flow.** Start one domain-named Flow at the API boundary for a multi-step mutation. Never chain dependent starts in the API handler. See [flow.md](references/core/flow.md#own-downstream-starts-durably).
- **Prefer parallel Steps to SubFlows.** Prefer parallel Steps inside one Flow, or a separate top-level Flow for a separate owner. Use a SubFlow when the child needs its own lifecycle, history, or retries, or when one fan-out is very large. See [subflow.md](references/core/subflow.md#when-to-use-a-subflow).
- **Dex-first state.** Default durable state to Attributes and AttributeMaps, not a new database. See [data-handling.md](references/core/data-handling.md#dex-first-state-ownership).
- **Start first.** Call StartFlow first with stable identity. Derive a stable Request ID from the complete logical start request. To attach matching retries and reject changed details for the same Flow ID, use an ID reuse policy that rejects reuse and ignore the already-started error, which applies only to a matching Request ID. Do not read before or after a successful start to guard duplicates. See [error-handling.md](references/core/error-handling.md#start-first-reconcile-only-after-an-error).
- **Typed failures.** Keep typed Dex errors until domain policy can tell a business rejection, a closed-Flow race, a retryable failure, and a local defect apart.
- **Terminal reads.** Read retained state with a Client Attribute read or a query-only RPC. Never decode Step outputs from history for a business read. Every result or status a user must see has such a read path: a result Attribute, or the Flow's completion output from WaitForFlow.
- **Open Flows.** Keep stable names compatible with open Flow runs. See [application.md](references/core/application.md#versioning-flow-code).

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
