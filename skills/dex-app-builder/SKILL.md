---
name: dex-app-builder
description: Primary entry point for designing and building Dex applications and process products with source-available Dex. Use for Dex product, application, or workflow requests, including converting an exported n8n workflow, but not standalone SDK or official connector-library work. Orchestrates business discovery, Flow and FDG modeling, the Dex Web or custom UI decision, a Go backend with released connectors, and local verification with dexcli dev.
---

# Dex App Builder

Build the smallest coherent Dex product that satisfies the user's request. A
clear implementation request authorizes its stated UI and routine engineering
choices. Ask only for missing business facts, access, or consequential
choices. Do not turn discovery, design, wireframes, or source checks into
repeated approval gates.

The toolchain is source-available Dex: the Dex Go SDK, `dexcli` (including
`dexcli dev` and the FDG analyzer behind `dexcli visualize`), Dex Server
(server-zd), Dex Web, and released official Connectors.

## Session start and routing

Run the Dex Skills version check that the [Dex SDK skill](../dex-sdk/SKILL.md)
describes, once per session. It must never block the task.

Confirm a writable workspace and file and command tools before you claim
implementation. Without them, provide discovery and a handoff for a
repository-backed host.

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
| 0. Workspace | Classify the repository, choose or keep the stack, install tools | [Workspace bootstrap](references/workspace-bootstrap.md) |
| 0'. Import | Convert an exported workflow, such as n8n JSON, before the normal stages | [Workflow import](references/workflow-import.md) and [n8n semantics](references/n8n-semantics.md) |
| 1. Discovery | Actors, roles, permissions, lifecycle, Flow boundaries, storage, Connectors, management mapping | [Product discovery](references/product-discovery.md) |
| 2. UI decision | No custom UI (Dex Web only) or Custom UI for a recorded gap | [UI workflow](references/ui-workflow.md) and [Dex Web v2](references/dex-web-v2.md) |
| 3. Backend | Flow design, Dex-first storage, Actions, Workers, API contract | [Backend implementation](references/backend-implementation.md) |
| 3a. FDG | Summary, Display, Actions, Start input, Step groups, diagnostics | [Go FDG authoring](references/fdg-authoring.md) |
| 3b. Connectors | Catalog discovery, Connector Steps, Triggers, `llm`, credentials | [Connector architecture](references/connector-architecture.md) |
| 4. Verify and hand off | Source gate, local `dexcli dev` stack, real journeys, packaging, handoff | [Verification and handoff](references/verification-handoff.md) |

Keep source readiness separate from configured real execution at every stage.
Never report compilation as provider or end-to-end success.

## Essential product constraints

These App Builder rules are stricter than the Dex SDK rules. They take
precedence.

- **Go backend only.** This is a product decision. Strict FDG 2.0 only, and
  Dex Web v2 metadata comes only from the Go analyzer. Connectors also ship
  for Python and TypeScript, but App Builder uses the Go packages.
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
  executions compatible across deploys.
- **Credentials stay outside.** Keep them out of code, Flow state, logs, and
  definitions. Locally, Dex Web configures Connector connections. A Go
  application reads them through the Connector SDK's `sdkgo/projectconfig`.
  An operator or hosting platform supplies the deployed configuration. Dex
  hosts none.
- **Authorization at the trusted boundary.** A browser permission selection
  grants nothing. Enforce actor permissions at the trusted server boundary.
  Use stable, granular Action permissions.
- Do not mutate another repository or publish or deploy without
  authorization.

## Source authority

Inspect the existing manifests and source before you select tools. An
existing project keeps its pinned Go toolchain, Dex SDK, `dexcli`, Server,
Connector, and frontend versions unless the user asks for an upgrade.

Use the current SDK and Connector sources as the truth for API names. Copy API
shapes from runnable examples, not from memory. Discover filenames before you
read module source. Batch independent read-only requests when the host
supports it. Implement once the required types and lifecycle are understood.
Let concrete compiler, FDG, or provider diagnostics drive further
investigation.

Volatile details live in the versioned Dex Developer reference: flags,
defaults, limits, configuration keys, error codes, and version differences.
Find the version the project pins (the Go SDK in `go.mod`, `dexcli version`,
the Server release), then open that version with the version selector in the
reference sidebar.

- [Developer reference overview and compatibility matrix](https://docs.superdurable.io/reference/)
- [Go SDK](https://docs.superdurable.io/reference/go/)
- [Dex Web](https://docs.superdurable.io/reference/web/), including the
  [Flow source analyzer](https://docs.superdurable.io/reference/web/flow-visualization)
- [CLI](https://docs.superdurable.io/reference/cli/)
- [Server](https://docs.superdurable.io/reference/server/)
- [Go examples](https://github.com/superdurable/dex/tree/main/examples/go)
- [Connector catalog](https://connectors.superdurable.io/catalog.yaml) and
  [Connector library](https://github.com/superdurable/dex-connectors-library)

## Proportionality

Keep the design proportional to the request. Do not add application-specific
tests, mocks, or framework dependencies only to satisfy a generic scaffold.
Existing or requested real acceptance remains required. Generate API packages
before resolving their imports. The host commit tool may own the final source
gate. Use focused checks while editing, then act on its actual diagnostics
instead of running the same full gate twice in a row.
