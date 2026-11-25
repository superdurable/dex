---
name: dex-app-builder
description: Primary entry point for designing and building Dex applications and process products with source-available Dex. Use for Dex product, application, or workflow requests, including converting an exported n8n workflow, and for requests to learn, get started with, set up, or understand Dex, but not standalone SDK or official connector-library work. Orchestrates business discovery, Flow and FDG modeling, the Dex Web or custom UI decision, a backend in Go, Python, TypeScript, Java, or Rust with released connectors, and local verification with dexcli dev.
---

# Dex App Builder

Build the smallest coherent Dex product that satisfies the user's request. A
clear implementation request authorizes its stated UI and routine engineering
choices. Ask only for missing business facts, access, or consequential
choices. Do not turn discovery, design, wireframes, or source checks into
repeated approval gates.

The toolchain is source-available Dex: the Dex SDK for the application's
language (Go, Python, TypeScript, Java, or Rust), `dexcli` (including
`dexcli dev` and the FDG analyzer behind `dexcli visualize`), Dex Server
(server-zd), Dex Web, and released official Connectors.

## Session start and routing

Run the [Dex Skills version check](../dex-sdk/references/core/plugin-version-check.md)
once per session. Read the lifecycle hook status first. Run its fallback
only when the status is `unavailable` or absent. The check never blocks the
task.

Confirm a writable workspace and file and command tools before you claim
implementation. Without them, provide discovery and a handoff for a
repository-backed host.

- A request to learn, install, set up, or understand Dex, with no concrete
  business process yet, follows [Getting started](references/getting-started.md).
- Standalone official Connector work belongs to
  [Connector Contributor](../dex-connector-contributor/SKILL.md).
- Standalone SDK implementation, debugging, or operations belongs to
  [Dex SDK](../dex-sdk/SKILL.md).
- Product work stays here. Load the [Dex SDK](../dex-sdk/SKILL.md) skill
  before you design or write any Flow code. It holds the Primitive rules and
  patterns that this skill does not repeat.
- A missing released Connector capability routes that contribution to
  Connector Contributor. The application itself stays here.
- Honor a specialist the user invokes explicitly.
- A missing sibling skill means an incomplete installation. Update the
  three-skill bundle. Do not search arbitrary caches or fall back to
  superseded skills.

## The workflow

Each stage has one reference. Read only the sections the next decision needs.
Record decisions in the application's own documentation and do not reread a
reference once its facts are recorded.

| Stage | Goal | Reference |
| --- | --- | --- |
| 0. Workspace | Classify the repository, choose or keep the language, install tools | [Workspace bootstrap](references/workspace-bootstrap.md) |
| 0'. Import | Convert an exported workflow, such as n8n JSON, before the normal stages | [Workflow import](references/workflow-import.md) and [n8n semantics](references/n8n-semantics.md) |
| 1. Discovery | Actors, roles, permissions, lifecycle, Flow boundaries, storage, Connectors, management mapping | [Product discovery](references/product-discovery.md) |
| 2. UI decision | No custom UI (Dex Web only) or Custom UI for a recorded gap | [UI workflow](references/ui-workflow.md) and [Dex Web](references/dex-web-v2.md) |
| 3. Backend | Flow design, Dex-first storage, Actions, Workers, API contract | [Backend implementation](references/backend-implementation.md) |
| 3a. FDG | Summary, Display, Actions, Start input, Step groups, diagnostics | [FDG authoring](references/fdg-authoring.md) |
| 3b. Connectors | Catalog discovery, Connector Steps, Triggers, `llm`, credentials | [Connector architecture](references/connector-architecture.md) |
| 4. Verify and hand off | Source gate, local `dexcli dev` stack, real journeys, packaging, handoff | [Verification and handoff](references/verification-handoff.md) |
| 4'. Managed Dex App | Repository rules, checks, import, and Live for Dex Cloud | [Managed Dex App](references/managed-dex-app.md) |

Before Stage 0 picks a language, ask whether the application runs locally only
or is imported into Dex Cloud as a Managed Dex App. A Managed Dex App starts
from the example repository's shape instead of Stage 0's bootstrap. Build it
to its repository rules, and hand
off with its checks and import steps instead of the local `dexcli dev` steps.
Import and publishing stay the user's steps.

Keep source readiness separate from configured real execution at every stage.
Never report compilation as provider or end-to-end success.

## Essential product constraints

These App Builder rules are stricter than the Dex SDK rules. They take
precedence.

- **One backend language, chosen in Stage 0.** Go, Python, TypeScript, Java,
  and Rust are equal choices. Stage 0 keeps an existing application's
  language, uses the language the user names, and otherwise uses TypeScript.
  The rest of the workflow is the same for every language.
- **Strict FDG 2.0, with the directives listed for the language.** Use only
  the `dex:` directives and options that the
  [Flow visualization reference](https://docs.superdurable.io/reference/web/flow-visualization)
  lists for the application's language. Skip a capability it does not list.
  Never invent syntax, and never ask the user to switch languages for it.
- **Dex Web owns management.** It owns Runs, Work Queue, Actions, edits,
  history, and diagnostics: `dexcli dev` locally, the standalone `dex-web`
  binary or image when deployed. Do not recreate management APIs or UI. A
  separate participant interaction is a valid custom UI. A backend
  requirement alone is not.
- **Dex is the default store.** Use typed Flow Attributes and exactly loaded
  AttributeMaps. Justify an external store with a concrete access or scale
  gap and one owner per fact. No speculative database, projection, or cache.
- **Flow boundaries.** Use parallel Steps for one lifecycle. Use independent
  top-level Flows for separate owners, and for child jobs with their own
  business identity. Use no SubFlows unless the Dex SDK
  SubFlow gate passes and the user confirms. Never split work whose parts
  would exchange RPCs or messages on frequent events, such as every
  operation, callback, or poll. Each call between Flows adds to both
  histories and costs an engine operation, even a read.
- **Effects only in Execute.** Put provider effects in `Execute`, with an
  idempotency identity, a bounded timeout, and recovery. `WaitFor` declares
  durable conditions. It performs no provider or Dex mutation.
- **Start identity.** A stable Flow ID, a complete request ID, an explicit
  ID reuse policy, and typed error recovery govern starts. An accepted start
  returns its known identity without a preflight or success-path state read.
  Streams are best-effort observations.
- **Design before code.** Define each Flow's identity, lifecycle, Steps,
  typed RPCs, every primitive, selective loads, locks, retention, retries,
  and uncertain-effect handling in the application before coding. Keep open
  Flow runs compatible across deploys.
- **Credentials stay outside.** Keep them out of code, Flow state, logs, and
  definitions. Locally, Dex Web configures Connector connections. The
  application reads them through the project configuration loader of its
  language's Connector SDK.
  An operator or hosting platform supplies the deployed configuration. Dex
  hosts none.
- **Authorization at the trusted boundary.** A browser permission selection
  grants nothing. Enforce actor permissions at the trusted server boundary.
  Use stable, granular Action permissions.
- Do not mutate another repository or publish or deploy without
  authorization.

## Match the business shape to a pattern

Before you design a Flow, find its shape below and read the matching section of [patterns.md](../dex-sdk/references/core/patterns.md). Each gist is the rule; the section adds the skeleton and edge cases.

| Business shape | Pattern | Gist |
| --- | --- | --- |
| Wait for a person's or system's decision, with a deadline or escalation | [Deferred choice](../dex-sdk/references/core/patterns.md#deferred-choice) | One waiting Step races the decision Channels and a Timer with AnyOf. Execute checks the Timer result, then routes. RPCs only publish. |
| Many callers add entries, replies, or events to the same record at once | [Inbound event loop](../dex-sdk/references/core/patterns.md#inbound-event-loop) | RPCs validate and publish to a Channel. One waiting Step consumes the batch, writes state, and loops back to itself. |
| Bursts of events per entity, each entity handled in order by one short Flow | [Drain an external Channel](../dex-sdk/references/core/patterns.md#drain-an-external-channel) | One stable Flow ID per entity, one active run at a time, messages handled in commit order. The RPC validates and publishes to a Channel; on a missing or inactive run the caller starts one, allowing reuse of a closed Flow ID. One Step handles each message and closes with ForceCompleteIfChannelsEmpty. No external queue. |
| Close or act after a period of inactivity | [Inactivity tracker](../dex-sdk/references/core/patterns.md#inactivity-tracker) | AnyOf(Timer(window), activity Channel). Activity loops back for a fresh Timer; a fired Timer acts. |
| A collection that grows without bound | [Sequentially chunked AttributeMap](../dex-sdk/references/core/patterns.md#sequentially-chunked-attributemap) | Archive bounded chunks. An append loads only the current chunk and a read only the requested page, never the whole map. With a retention window, archive the full chunk, delete expired chunks by key, and record the oldest retained token in one commit. |
| Keyed records with direct lookup | [Hash-partitioned AttributeMap](../dex-sdk/references/core/patterns.md#hash-partitioned-attributemap) | A stable hash of the key picks one of a fixed number of map instances; lock and load only that instance. |
| Wait for an external system to become ready or finish | [Polling](../dex-sdk/references/core/patterns.md#polling) | Poll inside one long-running Execute with short calls and heartbeats; never a Timer loop, a WaitFor, or retries as the loop. |
| Recurring work, or reminders until the recipient opts out | [Cron schedule](../dex-sdk/references/core/patterns.md#cron-schedule), [Reminder](../dex-sdk/references/core/patterns.md#reminder) | Cron: start the run and the next Timer wait together. Reminder: race the Timer with opt-out, then rearm after sending. |
| A failure that a person must resolve | [Manual recovery](../dex-sdk/references/core/patterns.md#manual-recovery) | The Execute failure route goes to a Step that waits on retry and skip Channels. |
| A deadline for the whole process | [Graceful timeout](../dex-sdk/references/core/patterns.md#graceful-timeout) | The Flow timeout and its timeout handler, not a competing Step. |
| Independent branches of one lifecycle, then a join | [Parallel Steps](../dex-sdk/references/core/patterns.md#parallel-steps) | GoToMany; a Join waits for all, the first, or n branches. |
| Branches take turns on shared multi-Step work | [Critical section](../dex-sdk/references/core/patterns.md#critical-section) | A handoff Step consumes the only token. Every application-controlled exit returns it, including failure and early exit. Protected Steps finish through a token-releasing path. |
| Child jobs tracked by their own number: looked up, retried, stopped, or started by others | [Top-level child Flows](../dex-sdk/references/core/flow.md#child-jobs-with-their-own-identity) | A Step starts each child through the Client with a stable Flow ID and Request ID. The child reports back through an RPC, or the parent polls its terminal state. The parent reconciles stopped children and children that finish after a quorum. |

## Rules agents most often miss

These Dex SDK rules apply even before you load it:

- **No wall clock in business logic.** Business deadlines, routing, decisions, and retry-stable identities never read the wall clock (`Date.now()`, `datetime.now()`, `time.Now`), and neither do WaitFor or RPC handlers. Use durable Timers for deadlines and the Flow start time from the Context for identities. Local time is only for operational measurement and attempt-local budgets such as an external call's timeout.
- **Deadlines race in one waiting Step.** When a decision has a deadline, race its decision Channels and Timer in one waiting Step and check the Timer result in Execute. A deadline for the whole Flow uses the Flow timeout. Never use a Timer-only Step that an RPC cancels, or a clock comparison.
- **Tests keep real durations.** Keep the business duration and skip the Timer through the Client. Never make a business deadline a parameter only so tests or demos can shorten it. Execute routes a provider-confirmed permanent rejection to the failure Step itself; provider errors the application treats as retryable use Step retry. Tests cover that path with a fake rejection and the production configuration; the retry-exhausted route needs no shortened test. See [testing.md](../dex-sdk/references/core/testing.md#required-scenarios).
- **The Flow owns decisions and status.** The caller supplies business facts, chooses IDs, starts Flows, calls RPCs, and reads. Steps and RPC handlers apply the business rules and write the resulting state. When an initial status depends on those rules, the first Step computes it. Pass business facts and configuration as start input or initial Attributes, and let the Flow compute its own decisions and rule-dependent initial status. See [client.md](../dex-sdk/references/core/client.md#the-flow-owns-decisions-and-status).
- **Frequent concurrent appenders go through a Channel.** Route them through an RPC that publishes to a Channel consumed by one writing Step; never have each RPC lock and rewrite a shared list. Synchronous read-modify-write by a few writers uses the same Attribute lock across writers.
- **One consumer contract per Channel.** Each Channel has one consumer contract: one Step type with one wait shape. For an event every waiter must receive, use one ChannelMap key per waiter and publish to each key.
- **Bound growing collections.** Keep them in a sequentially chunked AttributeMap, with a retention window when old entries expire, never in one Attribute rewritten on each change. Each append, whether in an RPC or in the Step that consumes a Channel, loads only the current chunk instance.
- **The graph reads from source.** Return analyzer-visible decisions from Execute, use targets the analyzer can resolve, compose Steps instead of subclassing them, and fix every `dexcli visualize` error diagnostic before handoff.
- **Directives on every local Step.** Give each local Step its `dex:group` and `dex:explanation` directives.
- **Long-running Steps set their time budget.** The default heartbeat timeout is short. If a call can go a full heartbeat timeout without a heartbeat or Dex Stream write, the attempt times out; raising only the method timeout keeps the heartbeat deadline. Set the heartbeat timeout to at least that call's method timeout, or heartbeat from a loop of short calls. Set the retry total duration explicitly: after a failure, it decides whether Dex schedules another attempt, and to allow several near-full attempts it is about maximum attempts times the method timeout plus backoff, as an estimate. It does not cut short a running attempt, so a business deadline for the Flow uses a durable Timer. The defaults fit short calls; see [step.md](../dex-sdk/references/core/step.md#time-bounds) and the language's StepOptions reference.
- **Child jobs with their own identity are top-level Flows.** SubFlow IDs derive from the parent. A child that people look up, retry, or stop by its own number is a top-level Flow that a Step starts with a stable Flow ID and Request ID, and whose result the parent receives durably. StopFlow starts no new handlers in the child, so it may never report, and children can finish after a quorum, so the parent reconciles both from the child's stored state and accepts reports idempotently. See [flow.md](../dex-sdk/references/core/flow.md#child-jobs-with-their-own-identity).
- **Domain Step names.** Name Step types after the work, never `Init`, `Start`, or `Process`. Give each fixed kind of work its own Step type, not a kind field.
- **Durable results.** Every result or status a user must see has a durable read path: a result Attribute, or the Flow's completion output.

## Source authority

Inspect the existing manifests and source before you select tools. An
existing project keeps its pinned language toolchain, Dex SDK, `dexcli`, Server,
Connector, and frontend versions unless the user asks for an upgrade.

Use the current SDK and Connector sources as the truth for API names. Copy API
shapes from runnable examples, not from memory. Discover filenames before you
read module source. Batch independent read-only requests when the host
supports it. Implement once the required types and lifecycle are understood.
Let concrete compiler, FDG, or provider diagnostics drive further
investigation.

Volatile details live in the versioned Dex Developer reference: flags,
defaults, limits, configuration keys, error codes, and version differences.
Find the version the project pins (the SDK in the project manifest or
lockfile, `dexcli version`, the Server release), then open that version with
the version selector in the reference sidebar. To read a docs page as text,
follow its **View as Markdown** link when available, then select the Markdown
variant for the application's language if the page lists one.

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

## Proportionality

Keep the design proportional to the request. Do not add application-specific
tests, mocks, or framework dependencies only to satisfy a generic scaffold.
Existing or requested real acceptance remains required. Generate API packages
before resolving their imports. The host commit tool may own the final source
gate. Use focused checks while editing, then act on its actual diagnostics
instead of running the same full gate twice in a row.
