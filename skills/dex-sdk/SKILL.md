---
name: dex-sdk
description: Implement, debug, test, and operate applications with the Superdurable Dex SDK in Python, Go, Java, TypeScript, or Rust. Use for standalone SDK work and when dex-app-builder or dex-connector-contributor loads it. Do not use as the primary workflow for Dex product, application, or business-process requests.
---

# Dex SDK

Build reliable applications through Dex's public programming model. Keep the user's experience in Dex terms and Dex APIs.

## Session start

Before the first substantive Dex-related response, follow the [Dex Skills version check](references/core/plugin-version-check.md) once. Prefer the lifecycle hook status. Never delay or block the task for it.

## Which skill

| Request | Skill |
| --- | --- |
| A Dex product, application, or business process, end to end | dex-app-builder. It loads this skill's core and Go references for the backend. |
| Add or change an official Connector, operation, Trigger, or UI unit | dex-connector-contributor |
| Implement, debug, test, or operate code that uses the Dex SDK | this skill |

If the user explicitly invokes this skill, stay in it.

## Mental model

A **Flow** is one durable business execution with a stable Flow ID. It is a graph of **Steps**. Each Step has **WaitFor**, which returns the Conditions to wait on, and **Execute**, which does the work and returns a decision: the next Steps, or how the Flow closes. Each method call is one commit boundary. Dex persists every committed change and retries failed methods, so the Flow survives Worker and Server restarts.

| Primitive | What it is | Key rule |
| --- | --- | --- |
| [Flow](references/core/flow.md) | durable execution with identity, schema, and RPCs | one Flow per business lifecycle; name it after the domain |
| [Step](references/core/step.md) | WaitFor plus Execute; options set durability, timeouts, retries, loads, locks | split Steps by recovery boundary; external effects idempotent |
| [Attribute](references/core/attribute.md) | typed durable state; AttributeMap for keyed instances | the default home of application state |
| [Channel](references/core/channel.md) | durable FIFO queue consumed by WaitFor | queued intent, not storage |
| [RPC](references/core/rpc.md) | typed synchronous handler on a Flow | the only external write path for Flow state |
| [Stream](references/core/stream.md) | best-effort, resumable messages | progress only; never the record |
| [Timer](references/core/timer.md) | durable delay Condition | business delays and deadlines; never for polling |
| [SubFlow](references/core/subflow.md) | child Flow started from WaitFor | not in a first design; pass the evolution gate |
| [Connector](references/core/connector.md) | released provider integration as Connector Steps and Triggers | credentials never enter Flow state |
| [Client](references/core/client.md) | the application's boundary to Dex | start, invoke, read, wait; no direct state write |

Components: [application (Worker and Client)](references/core/application.md), [server](references/core/server.md), [dexcli and local development](references/core/cli.md), [Dex Web](references/core/web.md), [External Attribute Sync](references/core/external-attribute-sync.md).

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
| Wait for an external system | the Polling pattern |

## Find the right reference version

Skills hold stable rules only. Exact API names, signatures, options, defaults, limits, configuration, and version differences live in the SDK doc comments and the versioned Developer reference.

1. Find the installed SDK version in the project manifest or lockfile: `go.mod` (Go), `pyproject.toml` or the lockfile (Python), `pom.xml` or `build.gradle` (Java), `package.json` or the lockfile (TypeScript), `Cargo.toml` or `Cargo.lock` (Rust). Note its minor version.
2. Open the Developer reference for that component. `https://docs.superdurable.io/reference/<component>/` is the latest version; components are `go`, `python`, `java`, `typescript`, `rust`, `server`, `cli`, and `web`.
3. Select the project's version with the version selector in the left sidebar, or open `https://docs.superdurable.io/reference/<component>/<version>/` directly. The [reference overview](https://docs.superdurable.io/reference/) has the compatibility matrix.
4. For the Server, the CLI, and Dex Web, use the version that is deployed or installed (`dexcli version`).

Rules:

- Preserve the installed SDK version unless the user asks to upgrade.
- Treat the project's source, lockfile, installed SDK doc comments, and the matching runnable [examples](https://github.com/superdurable/dex/tree/main/examples) as authoritative for exact code. Examples on the default branch track the newest release; check them against the installed version.
- If you cannot verify an exact API for the installed version, stop at the version-independent Flow model and ask to inspect the installed package. Never invent a Dex API.
- `dex-developer` is this skill's former name. If a direct `dex-developer` install is also present, tell the user to remove it and install the plugin, then start a new session. Do not delete it yourself.

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
| State, maps, indexes, search, hard-delete secrets | [attribute.md](references/core/attribute.md) |
| Queues, approvals, moving pending messages | [channel.md](references/core/channel.md) |
| RPC design, transactions, cancellation from an RPC | [rpc.md](references/core/rpc.md) |
| Progress, LLM token streaming | [stream.md](references/core/stream.md) |
| Delays, deadlines, SkipTimer | [timer.md](references/core/timer.md) |
| Child Flows | [subflow.md](references/core/subflow.md) |
| Provider integrations, local or deployed Connector configuration | [connector.md](references/core/connector.md) |
| Client calls, reads, waits, lifecycle APIs | [client.md](references/core/client.md) |
| Errors, recovery types, diagnosis, operator recovery | [error-handling.md](references/core/error-handling.md) |
| Large values, BlobCache, whether to add a database | [data-handling.md](references/core/data-handling.md) |
| SQL projection of Attributes | [external-attribute-sync.md](references/core/external-attribute-sync.md) |
| Pattern choice and skeletons, durable AI agents | [patterns.md](references/core/patterns.md) |
| Integration tests | [testing.md](references/core/testing.md) |
| Server behavior, search backends, deployment | [server.md](references/core/server.md) |
| dexcli, `dexcli dev`, visualization | [cli.md](references/core/cli.md) |
| Dex Web | [web.md](references/core/web.md) |
| Go FDG annotations, Summary and Display, Start forms, Actions | [FDG authoring](../dex-app-builder/references/fdg-authoring.md) in dex-app-builder |
| Design review and handoff | [self-check.md](references/core/self-check.md) |

## Model before implementation

For non-trivial work, run the [design review](references/core/self-check.md#design-review), then write the Flow model before code. These rules apply everywhere:

- **Names.** Name Flow types after their domain. Start every RPC name with a concrete action verb and its subject, such as `getQueuedMessages`. Prefer complete, precise names to short or generic ones such as `Get`, `Update`, `Manager`, `Data`, or `Handler`.
- **WaitFor versus Execute.** Execute does work; WaitFor waits durably. Keep every external mutation idempotent or compensatable, wherever it runs. Never poll a provider from WaitFor or with a Timer loop; use the [Polling pattern](references/core/patterns.md#polling).
- **Commit boundaries.** Each WaitFor, Execute, and RPC call commits separately. Give a provider action its own Step when its success deserves its own checkpoint, retry policy, failure route, or audit boundary.
- **One owning Flow.** Start one domain-named Flow at the API boundary for a multi-step mutation. Never chain dependent starts in the API handler. See [flow.md](references/core/flow.md#own-downstream-starts-durably).
- **No SubFlows by default.** Use parallel Steps in one lifecycle, or a separate top-level Flow for a separate owner. See [subflow.md](references/core/subflow.md#evolution-gate).
- **Dex-first state.** Default durable state to Attributes and AttributeMaps, not a new database. See [data-handling.md](references/core/data-handling.md#dex-first-state-ownership).
- **Start first.** Call StartFlow first with stable identity. Do not read before or after a successful start to guard duplicates. See [error-handling.md](references/core/error-handling.md#start-first-reconcile-only-after-an-error).
- **Typed failures.** Keep typed Dex errors until domain policy can tell a business rejection, a closed-Flow race, a retryable failure, and a local defect apart.
- **Terminal reads.** Read retained state with a Client Attribute read or a query-only RPC. Never decode Step outputs from history for a business read.
- **Open Flows.** Keep stable names compatible with running executions. See [application.md](references/core/application.md#versioning-flow-code).

## Complete the vertical slice

A change includes typed inputs, outputs, and failures; Flow and Step definitions; every persistence schema entry; constructor-injected dependencies; registry, Worker, and Client wiring; an application boundary; and a real Dex Server integration test with unique Flow IDs and bounded waits instead of sleeps.

Before handoff, run the [code check](references/core/self-check.md#code-check).

## Links

- Developer reference: https://docs.superdurable.io/reference/
- Runnable examples: https://github.com/superdurable/dex/tree/main/examples
- Connector catalog: https://connectors.superdurable.io/
