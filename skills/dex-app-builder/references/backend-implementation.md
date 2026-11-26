# Stage 2: design the Flow and the API contract, then implement the backend

Follow the [SDK reference routing](../SKILL.md#load-references) for
the application's language, chosen in
[workspace bootstrap](workspace-bootstrap.md#choose-the-language). Load only
the Core and language references that the approved design needs. They
hold the Primitive rules, design patterns, error handling, testing, and
Worker lifecycle. Do not restate them in the application.

The product constraints take precedence over the general SDK rules: external effects
only in `Execute`, no provider or Dex mutation in `WaitFor` except Step
execution local values (read-only, idempotent provider calls are allowed
there), and strict FDG 2.0
rendering with the directives the Flow visualization reference lists for the
language.

## Design before code

For a non-trivial Flow, write a short model in the application's design
documentation before you edit it:

- the Flow identity, typed input, and output;
- Steps and transitions, with each Step's commit boundary;
- Attributes, AttributeMaps, Channels, RPCs, Timers, and Streams;
- selective loads and locks;
- recovery, including uncertain effects, and StepOptions where the defaults
  do not fit;
- Connector boundaries.

Read [FDG authoring](fdg-authoring.md) before you edit a Flow. Prefer
parallel Steps inside one lifecycle. Use independently started top-level
Flows with typed RPCs or Channels only when the confirmed data lifecycle
needs another owner. Use a SubFlow when the child needs its own lifecycle,
history, or retries, or when one fan-out is very large; see
[SubFlow](core/subflow.md#when-to-use-a-subflow).

## Dex-first storage

Dex Flow state is the default durable application store.

| Need | Use |
| --- | --- |
| Cohesive Flow-owned current state | typed Attributes |
| Independently accessed records, keyed entities, growing collections | AttributeMaps with exact instance loads, bounded chunks, or partitions |
| Supported list and lookup paths | indexed Attributes and Dex search |
| Application mutations | typed RPCs |
| Queued intent from outside | Channels |
| Large durable values | Attributes; Dex offloads large values to blob storage |
| The same domain facts for several processes | one stable domain or entity Flow that owns them and exposes typed operations |

- Read committed Attributes and exact AttributeMap instances from the
  application API with the Client Attribute read (`Client.GetAttributes` in
  Go), in one call per response.
  It needs no Worker and reads a closed Run until it is purged.
- Use a typed read-only RPC instead when Flow code must compute the response,
  or when it needs pending Channel messages or a whole AttributeMap.
- Cross-Flow reuse alone never justifies a database or duplicated state in
  each process Flow.
- A short-lived secret a Flow must carry, such as an API key supplied at
  start, goes in a hard-delete Attribute (`dex.HardDeleteOnNull()` in Go). Delete
  it after its last use. Never index it, sync it, use it in an Action
  condition, or show it in Summary, Display, or anything else Dex Web shows.
  Return it only to callers authorized to hold it. Design the Step to request
  a fresh secret when the value is gone.

Do not add an external database, cache, ORM, outbox, or shadow read model only
because the product has durable data or may need more queries later. First
evaluate partitioning, chunking, exact loads, indexed Attributes, typed RPCs,
and Attribute Store sync when Dex stays authoritative. Add external storage
only for a confirmed need beyond Dex's model: complex or ad-hoc secondary
indexes, full-text or vector search, a hot record that cannot accept
Attribute-lock serialization, joins or transactions across independently
owned records, or large cross-Flow analytics. Name one authoritative owner
per fact and a synchronization and recovery design.

## Runs that can be measured and improved

Consider these when the business wants to measure runs: which succeeded, for
whom, at what cost, and what a different decision would have done.

- **Prefer outcomes visible in the Run.** Each business outcome the contract
  names, such as approved, paid, activated, replied, or resolved, can be a
  Step the Run enters, an Action it accepts, or an Attribute value. When the
  outcome happens in another system, record it when the application learns
  it, through a Trigger, an Action, or a Step.
- **Prefer scalar Attributes for segment facts.** A fact the business
  compares runs by, such as plan, region, source, tier, or customer size, can
  be a scalar Attribute set once when known, not only a field inside JSON or
  a display string. Index it only when a list needs it.
- **Prefer decisions as plain functions.** Routing, scoring, thresholds, lane
  choices, and Timer durations can come from plain functions of recorded
  facts and configuration. The Step calls the function and records its
  inputs, so the function can replay past runs under a changed rule.
- **Prefer model calls as their own Steps.** A model call in its own Step
  keeps its result and token usage in the Run's history, and a retry of a
  later call does not repeat it. When the user chose
  the `llm` Connector, use an `llm` Connector Step, as
  [text generation](connector-architecture.md#text-generation) describes.
  Otherwise the Step's `Execute` calls the model and records a safe result,
  the token usage, and the information recovery needs.
- **Fields keep their meaning across releases.** A release adds a new
  Attribute for new data instead of changing what an existing one means, so
  runs from different releases stay comparable.

Keep personal data in these Attributes to what the process needs.

## Human operations

- Model each human operation as a typed Action with exactly one permission
  (`dex.ActionRequiresPermission` in Go), where the Flow visualization
  reference lists Actions for the language. Use lowercase domain keys such as
  `refund.manage` or `refund.message`.
- The Action condition controls presentation only. Every Action RPC rechecks
  current business state before it changes durable state or publishes a
  Channel message. It takes business locks when that check and the effect
  must commit atomically.
- When a successful Step method or RPC writes or deletes an Action condition
  source, the Worker sends the Flow's complete Action permission mapping.
  The Server evaluates it atomically with the writes and adds newly matching
  permissions to the Flow's permission history.
- Permission history only grows. A matched permission stays for that Flow
  run, stays searchable after completion, and is not removed by later state
  changes. Removing or renaming a permission does not clear old history.
- Do not add projection-only locks to maintain that history.
- A changed Action definition is evaluated on the next condition-source
  write or in a new Flow. Treat an Action definition as a stable contract for
  open Flows.
- Permission history is discovery data. It never proves current
  eligibility or caller authorization. The trusted application boundary
  decides who may invoke the RPC.
- An RPC that requests provider work validates and commits application state,
  then returns a movement to the Step that does that work, such as a
  Connector Step. It never calls the provider.

When the user chose Dex Web, [Dex Web](dex-web-v2.md#actions-permissions-and-work-queue)
has how its Work Queue uses this history.

## Effects and waits

- Keep external effects in `Execute`, with a stable idempotency identity and
  query-first reconciliation of uncertain outcomes. Pass the cancellation
  signal into calls that accept it; give a blocking call that cannot observe
  cancellation its own timeout.
- `WaitFor` sets up a durable wait. It may use a read-only, idempotent call
  to get a deadline and return a Timer for it. Its only Dex state writes are
  Step execution local values. Provider writes run in `Execute`.
- Prefer handling a deterministic, non-retryable condition in `Execute`:
  route to the failure Step with an ordinary decision. Use the SDK's force-fail decision
  (`dex.ForceFail` in Go) only when the whole Flow must end at once. A
  returned error is retried for the whole retry budget. Return errors only
  for provider errors the application treats as retryable.

## Workers

Every Worker is a pull Worker. Follow the Dex SDK Worker lifecycle rules for
the language. The App Builder essentials:

- Set the Server address on both the Worker and the Client. There is no
  Worker address.
- Start the Worker without blocking the process's other work. Gate startup
  on a bounded wait for pull registration. Keep handling start errors. In Go,
  run `Worker.Start` on a goroutine and wait with `WaitPullRegistered(ctx)`.
- Readiness combines the Worker's pull registration state with the
  application's own lifecycle health. Stopping keeps the latched readiness,
  so mark shutdown separately.
- Start replacement Workers and wait for their registration before you stop
  old ones.
- When a release adds a Step type or RPC name, start the new Workers first.
  Keep old logic compatible for names the old Workers still register.
- A Step that no Worker has taken waits in the Server's queue. Plan Step
  retries and timeouts for unavailable Workers anyway.
- Long-running handlers record heartbeats within their heartbeat timeout.
- With Server authentication on, set a credential on both the Client and the
  Worker.

The Workers page of the language's SDK reference, such as the
[Go Worker reference](https://docs.superdurable.io/reference/go/workers),
has the options, defaults, and capacity rules.

## Connectors

Use this section for each external system the user chose to reach through a
released Connector in discovery. Fetch the published catalog and match exact
released capabilities before you write its integration code. [Connector
architecture](connector-architecture.md) has the procedure, the composition
rules, Trigger bindings, credentials, and the contribution path. For such a
system, never add an application-local provider client, webhook adapter, or
direct provider SDK call when a released capability exists. A missing
capability for a chosen public product is a Connector contribution and a
release blocker.

When the user declines a Connector for a system, the application calls it
from its own Step code in `Execute`. Each call keeps an idempotency identity
and recovery. Its credentials stay out of code and Flow
state.

## Application API contract

The application's own UI and its other callers reach the Flows through the
application's HTTP API. Design that contract in the same pass as the Flow and
any Connector boundaries. Map each operation to a Flow start, a typed RPC, a
query, or confirmed ingress. Define authentication and permission
enforcement, idempotency, asynchronous status, validation, response and error
shapes, retry, and terminal outcomes.

- The API contract describes the business boundary. It never exposes Dex
  Steps, Channels, Attributes, connection credentials, or other execution
  internals.
- Write it as an OpenAPI document, or in the project's existing contract
  format.
- Generate the server interfaces in the backend's language from it before you
  implement handlers, and a TypeScript client when the application has a
  frontend. The backend implements the generated interfaces.
- Never hand-write parallel transport types or hand-edit generated code.
  Follow the project's convention for committing or regenerating generated
  output.

Add only the operations the user's UI or a confirmed caller needs. When the
application has no UI of its own and no caller needs an API, it ships the
Worker alone. With the user's own UI, follow [UI workflow](ui-workflow.md) and
do not resume dynamic frontend work while the Flow, Connector, and
application boundary are being implemented.

## Local Dex stack

Show Dex Web early. As soon as a Flow source first renders with
`dexcli visualize --json`, even with warnings, and before
you finish implementation or tests:

1. Render every Flow file into one persistent definition directory:
   `dexcli visualize path/to/flow-source --json --out DIRECTORY/<flow-name>`.
2. Start one long-lived `dexcli dev` for the user in the background, on stable
   ports with persistent state. Pass that directory with
   `--flow-rendering-dir`, and any local Connector override a Connector
   change needs.
3. Give the user the Dex Web URL right away, so they can inspect the graph,
   Connectors, and Runs while you continue.
4. Start the application Worker against that stack as soon as it compiles, so
   Runs appear live.

Keep that stack running for the rest of the task. Re-render the graphs after
each Flow change. Restart that stack only when Dex Web does not pick up the
change. `dexcli dev` binds the next free port when a default port is taken
and prints the addresses it chose. Point the Worker and later CLI commands at
those addresses. Run automated tests on their own isolated stacks, with free
ports and temporary state, so they never disturb or replace the user's stack.
