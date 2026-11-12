# Dex Web as the management surface

Dex Web is the operator UI for Dex. App Builder treats it as the
first management interface, not merely a Flow inspector. This file holds the
rules an application must follow so Dex Web can manage it. The
[Dex Web reference](https://docs.superdurable.io/reference/web/dex-web) has
the flags, defaults, image details, page behavior, and `/api` contracts.

## How Dex Web runs

- `dexcli dev` starts server-zd and an embedded Dex Web on your machine.
- A deployment runs the standalone `dex-web` binary or the
  `superdurable/dex-web` image as its own process next to the Dex Server. The
  Server does not serve Dex Web, and `dex-web` never starts a Server. Pin a
  release tag of the image for a deployment.
- Dex Web reads Flow Definition Graph (FDG) JSON files from a definition
  directory. It does not read Go source.
- Every Dex Web setting is a flag or an environment variable. There is no
  YAML file.
- With Server authentication on, Dex Web needs its own FlowService
  credential. That credential does not authenticate Web users.
- Dex Web neither authenticates users nor enforces authorization. Run it
  behind a trusted boundary that authenticates users, maps roles to
  permissions, and limits which permissions it sends. Never expose Dex Web or
  the FlowService API to the public Internet.

The Summary, Display, and Action metadata exists only for Go. Validate every
Flow with the FDG 2.0 analyzer. Features that need these definitions appear
only when a definition applies. Their release status is in the
[Dex Web reference](https://docs.superdurable.io/reference/web/dex-web).

## How Dex Web derives the management UI

Model one business record as one Run.

| Dex Web surface | Comes from |
| --- | --- |
| List and search columns | indexed Attributes |
| Extra read-only list fields | `GetDexSummary` |
| Run detail fields and UI slots | `GetDexDisplay` |
| Simple field edits | editable scalar fields in `GetDexDisplay` |
| Buttons and forms | Action RPCs: label, typed input, condition, one permission |
| Work Queue | Action permission history |
| Start Flow form | the input type of the Step registered with `DefineStartStep` |
| Timeline and Flow canvas | Step groups and explanations, waits, history |

Dex Web loads a bounded window of recent history for a Run. Show the progress
of a long-running business process in Display fields, not through the
execution graph.

Design these contracts before you propose a custom management backend or UI.
[Go FDG authoring](fdg-authoring.md) has the exact syntax.

## Actions, permissions, and Work Queue

- An Action is a typed RPC with Action metadata. It declares exactly one
  permission. Use lowercase domain keys, such as `refund.manage`.
- The Action condition controls presentation only. Every Action RPC rechecks
  current business state before it changes durable state or publishes a
  Channel message. Use business locks when that check and the effect must
  commit atomically.
- When a successful Step method or RPC writes or deletes an Action condition
  source, the Go Worker sends the Flow's complete Action permission mapping.
  The Server evaluates it atomically with the writes and adds newly matching
  permissions to the Flow's Work Queue permission history.
- Permission history only grows. A matched permission stays for that Flow
  run, stays searchable after completion, and is not removed by later
  state changes. Removing or renaming a permission does not clear old
  history.
- Application Steps need no projection-only locks to maintain that history.
- A changed Action definition is evaluated on the next condition-source write
  or in a new Flow. Treat an Action definition as a stable contract for open
  Flows.
- Work Queue matches a Run when its history contains any requested
  permission. A match means an Action is or was available. Opening the Run
  rechecks current eligibility.
- **Working as** selects one permission to filter work. It authenticates no
  one and grants nothing. Dex Web's own permission check reads the list from
  the request, so it is a consistency check, not authorization.
- Permission history is discovery data. It is never authorization evidence
  and never proof that an Action is still eligible. The trusted application
  boundary decides who may invoke the RPC.

## Start Flow

- Dex Web offers **Start Flow** when the
  definition has a supported typed start schema. The operator enters a Flow ID and the typed input.
- The form comes from the start Step's input type. Use a concrete Go struct
  with JSON tags. A `map[string]any` field cannot drive the form.
- Do not put Worker addresses, actor permissions, or engine run IDs in
  business start input.
- Dex Web derives whether to skip `WaitFor` from the validated graph. Never
  rewrite an execute-only Step into a dummy `WaitFor` to work around start
  admission. Check the graph, source, Server, and Worker registration.
- Dex Web generates a new request ID for every start and uses the default ID
  reuse policy. A second submission of the same Flow ID is rejected. That
  rejection is not a recovery path. After an uncertain start response, read
  the Flow ID with the read-only `dexcli flow summary` before you submit
  again. A different Flow ID is a different business operation.
- Start Flow sends the FDG's Flow and Step type names. They must match the
  Worker's registrations. Fix a mismatch by pairing `dexcli` with the Go SDK,
  not by adding type-name overrides.
- Connector Trigger applications still use the real Trigger as their
  acceptance path.

## Local Connector setup

Dex Web configures Connector connections only in loopback `dexcli dev`, under
**Connectors**. A deployed Dex Web exposes no Connector setup, OAuth, or
credential storage.

- Automatic setup needs an operation-specific factory from an exact official
  published module, a static `ConnectionName`, and no local module
  replacement. A generic factory or unsupported dependency still renders but
  cannot be configured.
- Connector ID plus connection name is one identity. Two module versions for
  one identity are a blocking conflict.
- Connection credentials and Trigger binding configuration are separate
  records. Changing one binding does not change another Flow that reuses the
  connection.
- The local connection file holds plaintext development credentials. Never
  commit, upload, log, or copy it into Flow state. Use
  `dexcli dev --connector-config-dir DIRECTORY` to isolate a stack.
- The application reads settings, bindings, and operation configuration at
  startup, so a change needs an application restart. A credential
  replacement takes effect on the next provider call.
- Fields the release derives from verified claims or read-only provider
  pickers are not free-text inputs. Do not add duplicate configuration for
  them in the application.
- For Connector development only, `dexcli dev` accepts a local release
  override for one Connector. It shows as **Local override**. Never treat an
  override as a published release or commit it as a dependency. Never serve a
  working tree as the declared next version into the default Go module cache,
  because that copy persists and shadows the real release. Use the override,
  or a temporary `GOMODCACHE`.

[Connector architecture](connector-architecture.md#local-development-configuration)
covers how the application reads this configuration.

## QR capture

A user-sourced string Action input may add `capture:qr-code` to its
`dex:input` directive. No other capture value exists. An Attribute-sourced or
non-string field fails analysis. Dex Web keeps the text input and adds a
scanner. Manual entry, paste, and keyboard scanners keep working.

Camera access needs HTTPS or localhost, and an embedding host must allow the
camera in its Permissions Policy. Scanning grants no permission, satisfies no
condition, validates no business value, and does not bypass the RPC.

## Diagnose a Run in Dex Web

- Read the Step's routing and pending reason before you change Worker
  configuration.
- A Step waiting for a Worker of its type needs a Worker that registers that
  Step type. A Step waiting for locks is blocked by another lock holder.
- After an authorized time travel, the Run ID stays the same and a new run
  attempt starts. Record the run attempt with the Run ID in evidence.
- The [Dex Web debugging guide](https://docs.superdurable.io/reference/web/debugging)
  has the read-only workflow and the `dexcli` commands that save evidence.

## Source layout and validation

For each Flow, keep in one Go file: the Flow and Step definitions, Attributes
and other persistence declarations, Action input structs, `GetDexSummary`,
`GetDexDisplay`, Action handlers, every `dex:*` directive, transitions, and
Dex decisions.

Validate every Flow file, not only the one you edited, with the
[FDG check](fdg-authoring.md#focused-verification). Keep locally rendered
definitions in the stack's definition directory. Package the same validated
definitions for a deployed Dex Web as
[verification and handoff](verification-handoff.md#package-flow-definitions)
describes.
