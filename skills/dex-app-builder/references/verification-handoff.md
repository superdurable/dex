# Stage 3: build, verify, and hand off

Source readiness and real business acceptance are separate milestones. Report
them separately. A source gate never proves business execution. A missing test
service is never a pass. Never fabricate a deployment command, URL, or
success; report only observed identities and results.

## Implementation loop

- Use the project's own commands. Do not invent a target the project lacks.
- In a fresh checkout, restore locked dependencies first: the backend's
  packages with its package manager and, for a frontend, `npm ci` or the
  project's equivalent. A missing generator
  executable needs dependency restoration, not repeated failing builds or a
  package upgrade.
- For the application API, change the contract source, then regenerate. Never
  hand-edit generated code. When generated output is a build output, keep it
  ignored and never commit it.
- Generate API packages before you resolve their imports with the package
  manager, such as `go mod tidy` in Go.
- Replace starter or sample Flows with the first business feature instead of
  keeping them beside it. Remove unused sample Steps, RPCs, and
  registrations. Preserve unrelated business Flows and tests in imported or
  existing applications.
- Keep the backend's package manifest and lockfile, the frontend lockfile,
  the language toolchain, the `dexcli` expectation, and generators unchanged
  unless the user asked for that stack or dependency change.
- Run focused checks after each edit batch. When the host commit tool owns
  the full source gate, do not repeat that gate immediately before handoff.

## Source gate

Keep one project command that:

1. regenerates the application API code when the application has an API;
2. renders every Flow source file that the installed `dexcli` analyzes to
   FDG 2.0 and fails unless each result is `valid: true` (see
   [FDG authoring](fdg-authoring.md#focused-verification));
3. runs the project's own recorded format, lint, type check, and build
   commands for the backend;
4. builds the production backend and, when one exists, the frontend.

After an API change in an application with a frontend, check both production
artifacts. A backend build does not prove that the frontend's generated
imports still exist. A Managed Dex App keeps its required API contract and
checks.

## Tests: real dependencies or none

Default applications get no integration or browser test suites, mocks,
fixtures, or test-framework dependencies. Generate application tests only when
the user asks or the project already has tests. Fakes may test the
application's own branch logic. Claims about Dex durability, retries, or
provider behavior need the real Server and the real provider; otherwise
record them as unverified. A requested executed test of such a claim stays
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
3. When the application uses released Connectors, configure each Connector
   connection in Dex Web **Connectors**. Start the application with the
   launch command Dex Web displays.
4. Start Runs through the real entry point: the application API, a Connector
   Trigger, Dex Web **Start Flow**, or `dexcli flow start`.
5. Exercise each application API operation, waits, Timers, retries, and
   terminal outcomes. When the user chose Dex Web, also exercise Work Queue
   discovery with **Working as**, each Action, and each editable field.
6. Diagnose with Dex Web and the read-only `dexcli flow` inspection commands.
   Record the exact Flow IDs and Run IDs you observed, and the run attempt
   after any time travel.

`dexcli dev` runs server-zd, which dispatches only to pull Workers. Its flags
and Server overrides are in the
[CLI reference](https://docs.superdurable.io/reference/cli/).

## Optional correctness checks

Before handoff, ask the user whether they want to try two optional Dex
checks. Offer them; do not recommend them strongly or require them.

- Static lock check: for supported source, `dexcli visualize` can flag
  missing or unresolved required locks on writes and publishes, and Attribute
  writes that may lose updates without a declared lock requirement. The
  warnings leave the graph valid. See
  [Declared lock requirements](https://docs.superdurable.io/reference/web/flow-visualization#declared-lock-requirements).
- Lost-result test run: `dexcli dev` can drop finished Step and RPC results.
  A Step then runs again as after a Worker crash, and an RPC caller sees a
  failure. Rerun the local journeys, and any requested tests, against it.
  Check that each Flow reaches the same result, each external effect happens
  once, and a Step that runs again sees correct state. See
  [Worker result loss injection](https://docs.superdurable.io/reference/server/configuration#dev-worker-result-loss).

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
- entity reads after the Flow closes, through the Client Attribute read or typed
  read RPCs;
- Connector Trigger routing to Flows and RPCs, with correlation;
- Connector Steps: the happy path and each branch a requirement wires, with
  the Connector mock package's default answers; do not test unwired branches;
- RPC-to-Connector-Step routing when an RPC requests provider work;
- the application against any uncommitted local Connector override, plus that
  Connector package's own provider and real-Dex tests.

Test each application API operation and each retained webhook through the
real Worker. For each provider webhook the application receives itself,
also test that an event with an invalid signature or proof writes nothing,
and that a repeated delivery of one provider event ID takes effect once.

For external storage, test its authority boundary, synchronization lag, retry,
outage handling, and reconciliation. For an Attribute Store row-key template,
test the Flow ID to key mapping and an ID that does not match the template.

Only real dependency calls establish Dex durability, Worker replacement,
Timers, RPCs, retries, provider behavior, or end-to-end behavior.

## Package Flow definitions

Dex Web reads FDG JSON, not application source. Produce it with one project command
that renders every Flow source file through
`dexcli visualize SOURCE --json --out DIRECTORY/<flow-name>`
and fails when any result is not `valid: true`. Never ship a partial graph
written next to blocking diagnostics. Regenerate after every Flow change and
after upgrading `dexcli`, because the rendered type names must match the
Worker.

A deployed Dex Web reads the definitions from the Dex Server. Ship the
validated files with the Worker build and give them to the Worker, which
publishes them when it starts. A deployment script can publish them with
`dexcli` instead. The Server keeps one definition per Flow type. A definition directory for
Dex Web is optional; when it has a file for a Flow type, that file wins over
the Server's definition. Dex Web marks an invalid definition with its error and
keeps the others working. After deploying, confirm that Designs lists every
Flow type with its graph. The
[Flow visualization reference](https://docs.superdurable.io/reference/web/flow-visualization#definition-sources)
has the definition sources, and the Workers page of each SDK's reference,
such as the
[Go Workers reference](https://docs.superdurable.io/reference/go/workers#flow-definitions),
has the Worker setting.

## Deployment, only when requested

- Run the Dex Server (server-zd). When the user chose Dex Web, run the
  standalone `dex-web` binary or image as a separate process and point it at
  the Server's FlowService address.
- Before you deploy a Worker, run `dexcli version check --server ADDRESS`
  against the target Server. Workers only open outbound connections to it.
- Dex Web neither authenticates users nor enforces authorization. Run it
  behind a trusted boundary that authenticates users and maps roles to
  permissions. Keep the FlowService and Web ports unreachable from outside
  that boundary.
- Dex provides no deployed Connector configuration. An application with
  released Connectors reads it
  through the project configuration loader of its language's Connector SDK,
  as
  [Connector architecture](connector-architecture.md#deployed-configuration-and-credential-boundary)
  describes.
- For a Server upgrade, read the server-zd release notes and follow the
  [Server upgrade reference](https://docs.superdurable.io/reference/server/upgrades).
- The [Server reference](https://docs.superdurable.io/reference/server/)
  covers configuration, high availability, authentication, and Worker
  dispatch.

## Handoff checklist

Ensure:

- dependencies, the language toolchain, lockfiles, `dexcli` pairing, and commands
  match the recorded stack, or a user-approved deviation is recorded;
- every top-level Flow is justified by its owner, retention, independent
  waits or Timers, and terminal lifecycle;
- no two Flows exchange RPCs or messages on frequent events;
- parallel work prefers Steps, and every SubFlow has a child that needs its
  own lifecycle, history, or retries, or a very large fan-out, with defined
  input, output, cancellation, and failure handling;
- temporary validation, failure, and expiry state cannot pollute a
  long-lived authoritative owner;
- authorization starts with `admin`, Action permissions are granular, and
  every extra role is justified;
- the user's UI answer is recorded: their own UI, Dex Web, both, or neither;
- when the application has an API, its contract was designed with the Flow
  and Connector boundaries, its generated code reproduces, and the backend
  boundary implements the generated interfaces; an application with no UI
  and no caller may ship only its Worker, except a Managed Dex App, which
  keeps its required API contract;
- every webhook the application receives itself verifies the provider
  signature or equivalent proof, deduplicates by the stable provider event
  ID, and correlates the event to its Flow;
- with the user's own UI, its generated client and production bundle
  reproduce, the dynamic UI uses only the generated client, and polish
  followed a passing real journey;
- with Dex Web, the management mapping is recorded, and the user's own UI
  repeats a Dex Web management feature only when the user asked;
- secrets are absent from files, logs, generated values, and archives;
- the user's Connector answer is recorded for each external system;
- every Connector capability matrix records the catalog, exact capability,
  component tag, and immutable manifest URL;
- every public-provider boundary with a chosen Connector reuses a released
  capability or records the contribution and its release blocker;
- every internal-service boundary with a chosen Connector records the
  internal-library decision or why a generic HTTP Connector remains
  appropriate;
- every provider call without a Connector runs in `Execute` with an
  idempotency identity and recovery;
- each Connector Step has a static connection name, receives only its
  current result, and uses a pure `MapToOperationInput`;
- each Trigger binding has a static binding name, an application-owned
  filter and Flow ID resolver, and a typed target;
- an application with released Connectors starts with the local Connector
  configuration Dex Web writes and survives a Dex Web restart, or the gap is
  a stated blocker;
- `dexcli visualize` ran on every Flow source file, in every language, and
  every error diagnostic was fixed before handoff; every Flow renders to a
  `valid: true` FDG 2.0 definition through one project command, and the
  rendered type names match the Worker;
- every Step returns analyzer-visible decisions from Execute, uses targets
  the analyzer can resolve, and is composed rather than subclassed from
  another Step;
- who computes each decision and status matches the recorded design, which
  by default has the Flow compute them from the facts the caller supplies;
- deadlines that trigger work are durable Timers, and idempotency keys and
  business IDs never derive from the current time; a decision with a
  deadline races its Channels and Timer in one waiting Step that checks
  which Condition fired;
- tests, when present, preferably skip business Timers through the Client
  and keep the real durations;
- dependencies and Connector versions are pinned to exact releases, with no
  local path dependency, `go.work`, local `replace`, branch, pseudo-version,
  or commit SHA in the
  production dependency graph;
- for a deployment, credentials stay out of code and Flow state, replacement
  Workers register before old ones stop, and readiness combines registration
  with lifecycle health;
- the repository has a clean, reviewable commit.

Finish with:

- the confirmed business, UI, permission-boundary, and Connector
  decisions;
- implemented and verified behavior, with the local Dex Web URL;
- Connector release or contribution status;
- exact test evidence, including observed Flow IDs;
- the FDG check result;
- any [code check](core/self-check.md#code-check) item the code does not
  meet;
- remaining limitations, unimplemented integrations, and release blockers;
- when deployment was requested, the observed Server, definition,
  configuration, and real end-to-end identities.
