# Stage 4: build, verify, and hand off

Source readiness and real business acceptance are separate milestones. Report
them separately. A source gate never proves business execution. A missing test
service is never a pass. Never fabricate a deployment command, URL, or
success; report only observed identities and results.

## Implementation loop

- Use the project's own commands. Do not invent a target the project lacks.
- In a fresh checkout, restore locked dependencies first: Go modules and, for
  a frontend, `npm ci` or the project's equivalent. A missing generator
  executable needs dependency restoration, not repeated failing builds or a
  package upgrade.
- For Custom UI, change the API contract source, then regenerate. Never
  hand-edit generated Go or TypeScript. When generated output is a build
  output, keep it ignored and never commit it.
- Generate API packages before you resolve their imports with `go mod tidy`.
- Replace starter or sample Flows with the first business feature instead of
  keeping them beside it. Remove unused sample Steps, RPCs, and
  registrations. Preserve unrelated business Flows and tests in imported or
  existing applications.
- Keep `go.mod`, `go.sum`, the frontend lockfile, the Go toolchain, the
  `dexcli` expectation, and generators unchanged unless the user asked for
  that stack or dependency change.
- Run focused checks after each edit batch. When the host commit tool owns
  the full source gate, do not repeat that gate immediately before handoff.

## Source gate

Keep one project command that:

1. regenerates any API contracts;
2. renders every Flow source file to FDG 2.0 and fails unless each result is
   `valid: true` (see [Go FDG authoring](fdg-authoring.md#focused-verification));
3. runs `gofmt`, `go vet`, and `go build`;
4. builds the production backend and any frontend.

After an API change, check both production artifacts. A Go build does not
prove that the frontend's generated imports still exist.

## Tests: real dependencies or none

Default applications get no integration or browser test suites, mocks,
fixtures, or test-framework dependencies. Generate application tests only when
the user asks. Requested tests follow a real-dependency policy: no component
mocks, fake providers, or intercepted API responses. Trigger each case through
real APIs, or record the unverified invariant. A requested executed test stays
incomplete until its real dependencies pass. Connector-library verification
keeps its own rules.

Complete, checked source can be handed off without a private Connector key or
an isolated test stack. Record real execution as pending and finish the
requested journey once configuration exists. Repair actual source-gate
failures before handoff.

## Local verification with dexcli dev

Use the long-lived `dexcli dev` stack started during implementation (see
[backend implementation](backend-implementation.md#local-dex-stack)). Report
its Dex Web URL again at handoff.

1. Render every Flow into the stack's definition directory. Confirm each Flow
   type appears in Dex Web with its graph.
2. Run the Worker against the Server address that `dexcli dev` printed, so
   the FDG Flow and Step type names match the Worker's registrations.
3. Configure each Connector connection in Dex Web **Connectors**. Start the
   application with the launch command Dex Web displays.
4. Start Runs through the real entry point: a Connector Trigger, the
   application API, Dex Web **Start Flow**, or `dexcli flow start`.
5. Exercise Work Queue discovery with **Working as**, each Action and
   editable field, waits, Timers, retries, and terminal outcomes.
6. Diagnose with Dex Web and the read-only `dexcli flow` inspection commands.
   Record the exact Flow IDs and Run IDs you observed, and the run attempt
   after any time travel.

`dexcli dev` runs server-zd, which dispatches only to pull Workers. Its flags
and Server overrides are in the
[CLI reference](https://docs.superdurable.io/reference/cli/).

## Durable verification

This list guides requested runtime acceptance. It is not a requirement to
generate test scaffolding. Use the local stack and the real Worker whenever
behavior crosses a Client, Worker, wait, RPC, Channel, Timer, Stream, retry,
provider, or process boundary. Use deadline-based polling, never fixed sleeps.
Report Flow IDs and status on failure. Never hide or skip a failing check. The
Dex SDK testing rules cover start ordering, downstream start recovery, and
terminal reads. Follow the Dex SDK read-after-write rules for every write
followed by a read: assert a strongly consistent RPC read on the first read,
confirm triggered business work separately, and add no callbacks or polling to
strong write and read pairs.

Cover what applies:

- start and typed terminal output;
- duplicate start and request behavior, with reconciliation reads only in
  error branches, and no defensive reread after an accepted start;
- durable waits and Worker replacement;
- Worker lifecycle: startup registration gating, readiness, drain on stop,
  and idempotency under at-least-once delivery;
- with Server authentication on: Client, Worker, CLI, and Dex Web
  credentials, rejected missing credentials, and Web port protection;
- Action eligibility, a valid Action, a duplicate or late Action, and
  rejection after the Flow ends;
- role-to-permission mapping, unauthorized Action rejection, and
  multi-permission work discovery at the application boundary;
- concurrent Action-source writes without projection-only locks, and
  permission history after state changes and completion;
- retry and exhausted-retry recovery;
- provider idempotency and unknown-outcome reconciliation;
- Summary and Display reads before, during, and after completion;
- entity reads after the Flow closes, through `Client.GetAttributes` or typed
  read RPCs;
- Connector Trigger routing to Flows and RPCs, with correlation;
- Connector Steps: the happy path and each branch a requirement wires, with
  the Connector mock package's default answers; do not test unwired branches;
- RPC-to-Connector-Step routing when an RPC requests provider work;
- the application against any uncommitted local Connector `replace`, plus that
  Connector module's own provider and real-Dex tests.

For No custom UI, also assert that no approval, display, status, list,
search, detail, retry, escalation, Action-proxy, or Attribute-proxy route
remains. Test each retained webhook on its own.

For external storage, test its authority boundary, synchronization lag, retry,
outage handling, and reconciliation. For an Attribute Store row-key template,
test the Flow ID to key mapping and an ID that does not match the template.

Only real dependency calls establish Dex durability, Worker replacement,
Timers, RPCs, retries, provider behavior, or end-to-end behavior.

## Package Flow definitions

Dex Web reads FDG JSON, not Go source. Produce it with one project command
that renders every Flow source file through
`dexcli visualize SOURCE --schema-version 2.0 --json --out DIRECTORY/<flow-name>`
and fails when any result is not `valid: true`. Never ship a partial graph
written next to blocking diagnostics. Regenerate after every Flow change and
after upgrading `dexcli`, because the rendered type names must match the
Worker.

A deployed Dex Web reads the definitions from the Dex Server. Ship the
validated files with the Worker build and give them to the Worker, which
publishes them when it starts. A deployment script can publish them with
`dexcli` instead. The Server keeps one definition per Flow type. A definition
with a later generation time, recorded in the definition, replaces an older
one, whatever the upload order. A definition directory for
Dex Web is optional; when it has a file for a Flow type, that file wins over
the Server's definition. Dex Web marks an invalid definition with its error and
keeps the others working. After deploying, confirm that Designs lists every
Flow type with its graph. The
[Flow visualization reference](https://docs.superdurable.io/reference/web/flow-visualization#definition-sources)
has the definition sources, and each SDK's Workers reference, such as the
[Go Workers reference](https://docs.superdurable.io/reference/go/workers#flow-definitions),
has the Worker setting.

## Deployment, only when requested

- Run the Dex Server (server-zd) and the standalone `dex-web` binary or image
  as separate processes. Point Dex Web at the Server's FlowService address.
- Before you deploy a Worker, run `dexcli version check --server ADDRESS`
  against the target Server. Workers only open outbound connections to it.
- Dex Web neither authenticates users nor enforces authorization. Run it
  behind a trusted boundary that authenticates users and maps roles to
  permissions. Keep the FlowService and Web ports unreachable from outside
  that boundary.
- Dex provides no deployed Connector configuration. A Go application reads it
  through `sdkgo/projectconfig`, as
  [Connector architecture](connector-architecture.md#deployed-configuration-and-credential-boundary)
  describes.
- For a Server upgrade, read the server-zd release notes and follow the
  [Server upgrade reference](https://docs.superdurable.io/reference/server/upgrades).
- The [Server reference](https://docs.superdurable.io/reference/server/)
  covers configuration, high availability, authentication, and Worker
  dispatch.

## Handoff checklist

Ensure:

- dependencies, the Go toolchain, lockfiles, `dexcli` pairing, and commands
  match the recorded stack, or a user-approved deviation is recorded;
- every top-level Flow is justified by its owner, retention, independent
  waits or Timers, and terminal lifecycle;
- no two Flows exchange RPCs or messages on frequent events;
- parallel work stays in Steps, and every SubFlow has evidence, rejected
  alternatives, a defined parent-child lifecycle, and user confirmation;
- temporary validation, failure, and expiry state cannot pollute a
  long-lived authoritative owner;
- authorization starts with `admin`, Action permissions are granular, and
  every extra role is justified;
- the management UI mapping preceded the UI-mode decision, and the mode is
  recorded;
- every Custom UI surface names a remaining Dex Web gap; a No custom UI
  application exposes no business controls or management routes;
- a Custom UI's API contract was designed with the Flow and Connector
  boundaries, both generated clients and production bundles reproduce, the
  Go boundary implements the generated interfaces, the dynamic UI uses only
  the generated client, and polish followed a passing real journey;
- secrets are absent from files, logs, generated values, and archives;
- every Connector capability matrix records the catalog, exact capability,
  component tag, and immutable manifest URL;
- every public-provider boundary reuses a released capability or records the
  contribution and its release blocker;
- every internal-service boundary records the internal-library decision or
  why a generic HTTP Connector remains appropriate;
- each Connector Step has a static connection name, receives only its
  current result, and uses a pure `MapToOperationInput`;
- each Trigger binding has a static binding name, an application-owned
  filter and Flow ID resolver, and a typed target;
- the application starts with the local Connector configuration Dex Web
  writes and survives a Dex Web restart, or the gap is a stated blocker;
- every Flow renders to a `valid: true` FDG 2.0 definition through one
  project command, and the rendered type names match the Worker;
- dependencies and Connector versions are pinned to exact releases, with no
  `go.work`, local `replace`, branch, pseudo-version, or commit SHA in the
  production dependency graph;
- for a deployment, credentials stay out of code and Flow state, replacement
  Workers register before old ones stop, and readiness combines registration
  with lifecycle health;
- the repository has a clean, reviewable commit.

Finish with:

- the confirmed business, UI-mode, permission-boundary, and Connector
  decisions;
- implemented and verified behavior, with the local Dex Web URL;
- Connector release or contribution status;
- exact test evidence, including observed Flow IDs;
- the FDG check result;
- remaining limitations, unimplemented integrations, and release blockers;
- when deployment was requested, the observed Server, definition,
  configuration, and real end-to-end identities.
