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
- Product work stays here. Load Dex SDK when you implement the backend.
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
  top-level Flows for separate owners. Use no SubFlows unless the Dex SDK
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
the version selector in the reference sidebar.

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
