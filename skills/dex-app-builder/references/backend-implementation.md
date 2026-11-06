# Stage 3: design the Flow, Connectors, and API contract, then implement the backend

Load the [Dex SDK skill](../../dex-sdk/SKILL.md) and follow its routing with Go
as the only language. Load only the Core and Go references that the approved
design needs. Dex SDK holds the Primitive rules, design patterns, error
handling, testing, and Worker lifecycle. Do not restate them in the
application.

The App Builder constraints take precedence over Dex SDK: Go only, external
effects only in `Execute`, no provider or Dex mutation in `WaitFor`, and strict
FDG 2.0 rendering.

## Design before code

Answer the Dex SDK design self-check in the application's design
documentation. Then state, before you edit a Flow:

- the Flow identity, typed input, and output;
- Steps and transitions, with each Step's commit boundary;
- Attributes, AttributeMaps, Channels, RPCs, Timers, and Streams;
- selective loads and locks;
- retries, timeouts, and recovery, including uncertain effects;
- Connector boundaries.

Read [Go FDG authoring](fdg-authoring.md) before you edit a Flow. New
applications default to no SubFlows. Use parallel Steps inside one lifecycle.
Use independently started top-level Flows with typed RPCs or Channels only
when the confirmed data lifecycle needs another owner. Implement a SubFlow
only after the Dex SDK SubFlow gate passes and the user confirms it.

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
  application API with Go `Client.GetAttributes`, in one call per response.
  It needs no Worker and reads a closed Run until it is purged.
- Use a typed read-only RPC instead when Flow code must compute the response,
  or when it needs pending Channel messages or a whole AttributeMap.
- Cross-Flow reuse alone never justifies a database or duplicated state in
  each process Flow.
- A short-lived secret a Flow must carry, such as an API key supplied at
  start, goes in a hard-delete Attribute (`dex.HardDeleteOnNull()`). Delete
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
owned records, or large cross-Flow analytics. Then document the operation and
scale, one authoritative owner per fact, the synchronization and recovery
design, and tests for failure and reconciliation.

## Runs that can be measured and improved

Build every Flow so that its own history answers which runs succeeded, for
whom, at what cost, and what a different decision would have done. These rules
cost little now and are expensive to retrofit.

- **Outcomes are visible in the Run.** Each business outcome the contract
  names, such as approved, paid, activated, replied, or resolved, is a Step
  the Run enters, an Action it accepts, or an Attribute value. When the
  outcome happens in another system, record it when the application learns
  it, through a Trigger, an Action, or a Step.
- **Segment facts have their own scalar Attributes.** A fact the business
  compares runs by, such as plan, region, source, tier, or customer size, is
  a scalar Attribute set once when known. It is not only a field inside JSON
  or a display string. Index it only when a list needs it.
- **Decisions are plain functions.** Routing, scoring, thresholds, lane
  choices, and Timer durations come from plain functions of recorded facts
  and configuration. The Step calls the function and records its inputs. The
  same function can then replay past runs under a changed rule.
- **Model calls are their own Steps.** Each model call is an `llm` Connector
  Step, so its result and token usage are in the Run's history. See
  [text generation](connector-architecture.md#text-generation).
- **Fields keep their meaning across releases.** A release adds a new
  Attribute for new data instead of changing what an existing one means, so
  runs from different releases stay comparable.

Keep personal data in these Attributes to what the process needs.

## Human operations

- Model each human operation as a typed Go Action with one
  `dex.ActionRequiresPermission` option. Use lowercase domain keys such as
  `refund.manage` or `refund.message`.
- An Action RPC rechecks current state. It takes business locks when its
  state check and effect must commit atomically.
- Do not add projection-only locks. The Server maintains Work Queue
  permission history atomically. A historical match supports discovery. It
  never proves current eligibility or caller authorization.
- An RPC that requests provider work validates and commits application state,
  then returns a movement to a Connector Step. It never calls the provider.

[Dex Web v2](dex-web-v2.md#actions-permissions-and-work-queue) has the full
permission model.

## Effects and waits

- Keep external effects in `Execute`, with a stable idempotency identity, a
  bounded timeout, and query-first reconciliation of uncertain outcomes.
- `WaitFor` only declares durable conditions. It never queries or mutates
  providers or Dex state, and it never computes or persists a deadline.
- Fail a deterministic, non-retryable condition with `dex.ForceFail`. A
  returned error is retried for the whole retry budget. Return errors only
  for transient faults.

## Workers

Every Go Worker is a pull Worker. Follow the Dex SDK Worker lifecycle rules.
The App Builder essentials:

- Set the Server address on both the Worker and the Client. There is no
  Worker address.
- Run `Worker.Start` on a goroutine. Gate startup on a bounded
  `WaitPullRegistered(ctx)`. Keep handling `Start` errors.
- Readiness combines `PullRegistrationState().IsReady` with the application's
  own lifecycle health. `Stop` keeps the latched readiness, so mark shutdown
  separately.
- Start replacement Workers and wait for their registration before you stop
  old ones.
- When a release adds a Step type or RPC name, start the new Workers first.
  Keep old logic compatible for names the old Workers still register.
- A Step that no Worker has taken waits in the Server's queue. Plan Step
  retries and timeouts for unavailable Workers anyway.
- Long-running handlers record heartbeats within their heartbeat timeout.
- With Server authentication on, set a credential on both the Client and the
  Worker.

The [Go Worker reference](https://docs.superdurable.io/reference/go/workers)
has the options, defaults, and capacity rules.

## Connectors

Fetch the published catalog and match exact released capabilities before you
write any provider integration code. [Connector
architecture](connector-architecture.md) has the procedure, the composition
rules, Trigger bindings, credentials, and the contribution path. Never add an
application-local provider client, webhook adapter, or direct provider SDK
call when a released capability exists. A missing capability for a public
product is a Connector contribution and a release blocker.

## Custom UI API contract

For Custom UI, design the application HTTP API contract in the same pass as
the Flow and Connector boundaries. Follow
[UI workflow](ui-workflow.md#2-contract-and-backend-design): an OpenAPI or
existing contract source, generated Go server interfaces and TypeScript
client, no hand-written transport types, and no Dex internals in the
contract. Do not resume dynamic frontend work while the Go Flow, Connector,
and application boundary are being implemented.

## Local Dex stack

Show Dex Web early. As soon as a Flow source first renders with
`dexcli visualize --schema-version 2.0 --json`, even with warnings, and before
you finish implementation or tests:

1. Render every Flow file into one persistent definition directory:
   `dexcli visualize path/to/flow.go --schema-version 2.0 --json --out DIRECTORY/<flow-name>`.
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
