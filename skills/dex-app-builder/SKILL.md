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
repeated approval gates.

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
- **External stores need a recorded gap.** Justify a store outside Dex with
  a concrete access or scale gap and one owner per fact. No speculative
  database, projection, or cache.
- **Flow boundaries.** A SubFlow also needs the user's confirmation after
  the gate passes. Never split work whose parts
  would exchange RPCs or messages on frequent events, such as every
  operation, callback, or poll. Each call between Flows adds to both
  histories and costs an engine operation, even a read.
- **No mutation in WaitFor.** `WaitFor` mutates no provider and writes no Dex
  state except Step execution local values, which record a read error or pass
  a read value to `Execute`. It may
  make read-only, idempotent, time-bounded provider calls. Connector write
  operations run in `Execute`. Each provider effect in `Execute` has an
  idempotency identity, a bounded timeout, and recovery.
- **Complete start identity.** Each start has a complete request ID, an
  explicit ID reuse policy, and typed error recovery.
- **Record the design.** Record the Flow model in the application's own
  documentation before coding, including selective loads, locks, retention,
  retries, and uncertain-effect handling.
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
| [SubFlow](references/core/subflow.md) | child Flow started from WaitFor | not in a first design; pass the evolution gate |
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
- Write Flow state only through typed RPCs. Read stored Attributes with the Client Attribute read, and use a read-only RPC when the read needs Flow code, pending messages, or a whole AttributeMap. Wait with Attribute match, not polling.
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
| Take replies until a resolution arrives or the conversation goes idle | [Idle Timer with Resolution](references/core/patterns.md#idle-timer-with-resolution) (Inbound event loop) | RPCs validate and publish. One Step waits on replies, the resolution, and the idle Timer, and routes on the winning Condition: replies are recorded and the Step loops back for a fresh Timer. |
| Bursts of events per entity, each entity handled in order by one short Flow | [Drain an external Channel](references/core/patterns.md#drain-an-external-channel) | One stable Flow ID per entity, one active run at a time, messages handled in commit order. The RPC validates and publishes to a Channel; on a missing or inactive run the caller starts one, allowing reuse of a closed Flow ID. One Step handles each message and closes with ForceCompleteIfChannelsEmpty. No external queue. |
| Close or act after a period of inactivity | [Idle Timer](references/core/patterns.md#idle-timer) (Inactivity tracker) | AnyOf(Timer(window), activity Channel). Activity loops back for a fresh Timer; a fired Timer acts. |
| A collection that grows without bound | [Sequentially chunked AttributeMap](references/core/patterns.md#sequentially-chunked-attributemap) | Archive bounded chunks. An append loads only the current chunk and a read only the requested page, never the whole map. Bound the total: delete the oldest chunks by count or age, archiving them first when the business must keep them, and record the oldest retained token in the same commit. |
| Keyed records with direct lookup | [Hash-partitioned AttributeMap](references/core/patterns.md#hash-partitioned-attributemap) | A stable hash of the key picks one of a fixed number of map instances; lock and load only that instance. |
| Wait until a deadline that a provider supplies, such as a due date | [Timer](references/core/timer.md) | Read it in WaitFor with a read-only, idempotent, time-bounded call and return the Timer; the Timer is durable once WaitFor's result commits. Read it instead in the previous Step's Execute and pass it as the next Step's input when the value must be auditable or visible, or needs its own failure route. |
| Wait for an external system to become ready or finish | [Polling](references/core/patterns.md#polling) | Either a bounded WaitFor Timer loop with one short provider call per Execute, which records each round in the history and is somewhat heavier, or one long-running Execute with short calls and heartbeats, which is lighter. Never call the provider in WaitFor or use retries as the loop. |
| Recurring work, or reminders until the recipient opts out | [Cron schedule](references/core/patterns.md#cron-schedule), [Reminder](references/core/patterns.md#reminder) | Cron: start the run and the next Timer wait together. Reminder: race the Timer with opt-out, then rearm after sending. |
| A failure that a person must resolve | [Manual recovery](references/core/patterns.md#manual-recovery) | The Execute failure route goes to a Step that waits on retry and skip Channels. |
| Undo earlier Steps after a Step fails | [Execute failure recovery](references/core/patterns.md#execute-failure-recovery) | The failure route compensates, starting with the failed Step itself, because a Step whose retries ran out may still have taken effect. |
| A deadline for the whole process | [Graceful timeout](references/core/patterns.md#graceful-timeout) | The Flow timeout and its timeout handler, not a competing Step. |
| Independent branches of one lifecycle, then a join | [Parallel Steps](references/core/patterns.md#parallel-steps) | GoToMany; a Join waits for all, the first, or n branches. |
| Branches take turns on shared multi-Step work | [Critical section](references/core/patterns.md#critical-section) | A handoff Step consumes the only token. Every application-controlled exit returns it, including failure and early exit. Protected Steps finish through a token-releasing path. |
| Child jobs tracked by their own number: looked up, retried, stopped, or started by others | [Top-level child Flows](references/core/flow.md#child-jobs-with-their-own-identity) | A Step starts each child through the Client with a stable Flow ID and Request ID. The child reports back through an RPC, or the parent polls its terminal state. When the parent's result depends on how a stopped or late child ended, it reconciles that child; otherwise it stops the child and moves on. |

## Rules agents most often miss

- **Deadlines use durable Timers, not clock comparisons.** A business deadline is a durable Timer, never `now() > deadline` in a Step or an RPC, so that it is durable, visible in the graph, and skippable in tests. Idempotency keys and business IDs never derive from the current time, which differs per retry; use the Flow ID and the Flow start time from the Context. Reading the time to record a timestamp or compute a schedule is fine: the committed value is the one that counts.
- **Deadlines race in one waiting Step.** When a decision has a deadline, race its decision Channels and Timer in one waiting Step. In Execute, check which Condition fired; never infer "deadline passed" from "no Channel result". A decision already queued when the wait starts wins at once; otherwise the event that commits first wins. A decision can still commit after the Timer won and before Execute runs, and its RPC returns success. When the business must decide "no reply before the deadline", close the reply window with an Attribute lock: see [Close a reply window with an Attribute lock](references/core/patterns.md#close-a-reply-window-with-an-attribute-lock). Declare decision Channels in business priority order. A deadline for the whole Flow uses the Flow timeout. Never use a Timer-only Step that an RPC cancels.
- **Tests keep real durations.** Keep the business duration and skip the Timer through the Client. Never make a business deadline a parameter only so tests or demos can shorten it. A fake permanent rejection under the production configuration verifies that branch and that it does not retry; in product work, requested tests trigger it through the real provider or record the branch as unverified, as in [verification-handoff.md](references/verification-handoff.md#tests-real-dependencies-or-none). A direct test verifies the recovery Step's logic. Dex scheduling the recovery Step after the real retry budget is not re-tested with shortened durations. See [testing.md](references/core/testing.md#required-scenarios).
- **Prefer calls that can fail in Execute.** WaitFor returns the Conditions to wait on and never mutates an external system. It may make read-only, idempotent, time-bounded calls, such as reading a due date for a Timer; every provider mutation, including Connector write operations, runs in Execute. If a WaitFor read gets a permanent error, record it with a Step execution local value, return SkipWaitImmediately, and let Execute route on it; never let a permanent error loop in Step retry. When a provider confirms a permanent error, Execute routes to a failure-handling Step or ForceFails the Flow. Errors that can succeed on a later attempt use Step retry. See [step.md](references/core/step.md#waitfor-versus-execute) and the language's Step reference, such as [Go](https://docs.superdurable.io/reference/go/step#step-execution-local).
- **Race branches as Steps, not inside one Execute.** Never race several external calls or business branches inside one Execute with in-process concurrency, such as `Promise.any`, asyncio tasks, or threads. Use GoToMany with a sibling cancel, or a Join for the first k of n, so each branch has its own retries, failure route, and durable result. See [First-win parallel Steps](references/core/patterns.md#first-win-parallel-steps).
- **Frequent concurrent appenders go through a Channel.** Route them through an RPC that publishes to a Channel consumed by one writing Step; never have each RPC lock and rewrite a shared list. Synchronous read-modify-write by a few writers uses the same Attribute lock across writers.
- **One active consumer per Channel at a time.** At any moment, at most one waiting Step consumes a given Channel, so which Step receives a message never depends on timing. Different Step types may consume the same Channel in different phases of the Flow. For an event every waiter must receive, use one ChannelMap key per waiter and publish to each key.
- **Bound growing collections.** Keep them in a sequentially chunked AttributeMap, never in one Attribute rewritten on each change. Each append loads only the current chunk, and each read loads only the requested page. Also bound the total: delete the oldest chunks by count or age, after archiving them when the business must keep them. Size limits are in the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits).
- **The graph reads from source.** Return analyzer-visible decisions from Execute, use targets the analyzer can resolve, compose Steps instead of subclassing them, and fix every `dexcli visualize` error diagnostic before handoff.
- **Long-running Steps set their time budget.** The default heartbeat timeout is short. If the attempt reaches its heartbeat deadline without a heartbeat or a Dex Stream write, Dex times out the attempt; raising only the method timeout keeps that deadline. For a call that can stay silent that long, set the heartbeat timeout to at least that call's method timeout, or heartbeat from a loop of short calls, with a margin over the longest silent interval. Set the retry total duration explicitly: after a failure, it decides whether Dex schedules another attempt, and to allow several near-full attempts it is about maximum attempts times the method timeout plus backoff, as an estimate. It does not cut short a running attempt, so a deadline for the whole Flow uses the Flow timeout, and a deadline for one wait uses a durable Timer Condition. The defaults fit short calls; see [step.md](references/core/step.md#time-bounds) and the language's StepOptions reference.
- **Compensation starts with the failed Step.** A Step whose retries ran out may still have taken effect, for example when the provider applied the change and then timed out. Compensation starts with that Step: undo it with its original reference, and make every undo a no-op for an action that never happened. See [Execute failure recovery](references/core/patterns.md#execute-failure-recovery).
- **Child jobs with their own identity are top-level Flows.** SubFlow IDs derive from the parent. A child that people look up, retry, or stop by its own number is a top-level Flow that a Step starts with a stable Flow ID and Request ID, and whose result the parent receives durably and accepts idempotently. StopFlow starts no new handlers in the child, so it may never report. When the parent's result or a follow-up action depends on how a stopped child ended, such as counting its result, undoing its side effects, or showing its outcome, the parent reconciles it from the child's stored state; children that finish after a quorum are handled the same way. When it does not, the parent stops the child and moves on. See [flow.md](references/core/flow.md#child-jobs-with-their-own-identity).

## Model before implementation

For non-trivial work, run the [design review](references/core/self-check.md#design-review), then write the Flow model before code. These rules apply everywhere:

- **Names.** Name Flow and Step types after their domain, such as `ReserveStock`, never `Init`, `Start`, or `Process`. Give each fixed kind of work its own Step type, not a kind field. Start every RPC name with a concrete action verb and its subject, such as `getQueuedMessages`. Prefer complete, precise names to short or generic ones such as `Get`, `Update`, `Manager`, `Data`, or `Handler`.
- **WaitFor versus Execute.** WaitFor sets up a durable wait; any provider call there is read-only, idempotent, and time-bounded. Provider writes run in Execute, as **Prefer calls that can fail in Execute** states. Keep every provider write idempotent or compensatable. Never poll a provider from WaitFor; use a [Polling pattern](references/core/patterns.md#polling).
- **Commit boundaries.** Each WaitFor, Execute, and RPC call commits separately. Give a provider action its own Step when its success deserves its own checkpoint, retry policy, failure route, or audit boundary.
- **Prefer that the Flow owns decisions and status.** Pass business facts as start input or initial Attributes and let Steps compute decisions and status. Then the rules live in one place, and status changes commit together with the Flow's progress. A caller that precomputes a decision, such as "needs a second approval", spreads the rule across two places. See [client.md](references/core/client.md#the-flow-owns-decisions-and-status).
- **One owning Flow.** Start one domain-named Flow at the API boundary for a multi-step mutation. Never chain dependent starts in the API handler. See [flow.md](references/core/flow.md#own-downstream-starts-durably).
- **No SubFlows by default.** Use parallel Steps in one lifecycle, or a separate top-level Flow for a separate owner. See [subflow.md](references/core/subflow.md#evolution-gate).
- **Dex-first state.** Default durable state to Attributes and AttributeMaps, not a new database. See [data-handling.md](references/core/data-handling.md#dex-first-state-ownership).
- **Start first.** Call StartFlow first with stable identity. Do not read before or after a successful start to guard duplicates. See [error-handling.md](references/core/error-handling.md#start-first-reconcile-only-after-an-error).
- **Typed failures.** Keep typed Dex errors until domain policy can tell a business rejection, a closed-Flow race, a retryable failure, and a local defect apart.
- **Terminal reads.** Read retained state with a Client Attribute read or a query-only RPC. Never decode Step outputs from history for a business read. Every result or status a user must see has such a read path: a result Attribute, or the Flow's completion output from WaitForFlow.
- **Open Flows.** Keep stable names compatible with open Flow runs. See [application.md](references/core/application.md#versioning-flow-code).

## Complete the vertical slice

A change includes typed inputs, outputs, and failures; Flow and Step definitions; every persistence schema entry; constructor-injected dependencies; registry, Worker, and Client wiring; an application boundary; and a real Dex Server integration test with unique Flow IDs and bounded waits instead of sleeps.

Before handoff, run the [code check](references/core/self-check.md#code-check).

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
