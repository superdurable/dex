# Application: Worker and Client

A Dex application is ordinary application code plus two Dex components:

- the **Worker**, which hosts the registered Flow, Step, and RPC implementations;
- the **Client**, which starts and interacts with Flows. See [client.md](client.md).

A process can run a Worker, a Client, or both. The application keeps its own HTTP, messaging, database, and third-party integrations.

## Topology

```text
application API  --Client-->  Dex Server  <--outbound task streams--  Worker
dexcli, Dex Web  ------------>  Dex Server
```

- Dex Server receives Client calls and dispatches handler work to Workers.
- Every Worker is a pull Worker. It opens outbound task streams to the Server and never listens for incoming calls. It needs no inbound port.
- The Worker and the Client each need only the Dex Server address. There is no Worker address, bind address, or target to configure, and Flow configuration cannot change Worker routing.
- Dex Web and dexcli use the same Server API.

## Wiring

- Each Worker pool's registry registers every Flow type that pool serves, with every Step, RPC, Attribute, Channel, and Stream in its persistence schema; the Client's registry includes every Flow type it calls.
- Construct the Worker and the Client from the same registry, payload codec and keys, and blob cache configuration. See [payload encryption](data-handling.md#sensitive-data-and-payload-encryption).
- Concurrent executions share Flow and Step objects, so keep them free of per-request mutable state.
- A started Flow's Steps wait durably for a Worker, but an RPC fails when no Worker takes it. So before serving requests that call RPCs or wait on results, start the Worker and wait for its registration.
- Shut down in this order before process exit: stop admitting new requests, stop the Worker and wait for its handlers to drain, then close the Client and the blob cache. See [Graceful shutdown](#graceful-shutdown).
- Indexed Attributes are registered when the Worker starts. A registration the Server rejects stops the Worker from starting.
- Give the Worker the Flow Definition Graphs of its Flows. It publishes them to the Server when it starts, and Dex Web reads them there. See [web.md](web.md#flow-definition-graphs).

## First vertical slice

Build the smallest end-to-end path first:

1. Define one typed start input and one start Step.
2. Make the Step return a terminal decision with typed output.
3. Define the Flow's Step list and persistence schema.
4. Register the Flow in the registry.
5. Create one Worker with the registry and the Server address.
6. Create one Client with the same registry and payload configuration.
7. Start the Worker and wait for registration.
8. Define the start identity, duplicate-start behavior, and error outcomes. See [error-handling.md](error-handling.md).
9. Start the Flow from an HTTP handler, CLI command, or service method.
10. When the project has tests or the user asks for them, add a real-Server integration test that starts the Flow and waits for its result.

Usually add Attributes, Channels, RPCs, Streams, or Connectors after this path runs.

A complete change includes typed inputs, outputs, and failures; Flow and Step definitions; every persistence schema entry; constructor-injected dependencies; registry wiring; Worker and Client wiring; and an application boundary. When the project has tests or the user asks for them, it also includes a real Dex Server integration test.

## Dispatch

- Each Worker serves one task queue (the default task queue when it names none). Inside it, the Worker opens streams for its app Worker ID and every Flow type it registers. Within one task queue, a Flow type name must mean one Flow definition.
- The app Worker ID identifies one process and must change after every restart. Let the SDK generate it unless the deployment needs an explicit process-unique value.
- Dex first offers a Flow's work to the Worker that last served it, then to any Worker of the Flow type. When the previous process has no connected streams, Dex goes straight to the Flow type. Sticky routing does not promise the same process, so state a later handler needs lives in Dex ([Must 2](../../SKILL.md#must)). Sticky routing stays inside the Flow's task queue; it never crosses task queues.
- A Step that no Worker takes waits durably in Dex until a Worker takes it. It consumes no retry. A closing Flow timeout, a stop, another close, or a cancellation of the Step ends the wait; without a Flow timeout it can wait forever.
- A Step type that no connected Worker registers also waits, with its attempt number, snapshot, and locks, without blocking other Step types.
- An RPC never queues. It fails with UNAVAILABLE when no Worker takes it within the dispatch wait, or with FAILED_PRECONDITION naming the RPC when no connected Worker registers it.
- A Worker that does not register a requested Step type or RPC name declines it before any application code runs. Dex tries another Worker without consuming an attempt.

## Capacity

- A Worker's slot count is its maximum number of concurrent tasks. Dex sends a task only to a free slot, so a Worker never declines work for capacity. A Step that finds every Step slot busy waits in the task queue.
- An RPC fails when no Worker takes it within the dispatch wait. When Steps hold slots for minutes, leave capacity for RPCs, so they still run while Steps fill the Worker. RPC slot reservation, stream modes, and the Server's caps are in the [Server Workers reference](https://docs.superdurable.io/reference/server/workers).
- Scale Workers on the Server's task queue depth and oldest-age metrics. See the [Server Workers reference](https://docs.superdurable.io/reference/server/workers#step-task-queue).
- Long-running tasks keep their stream alive with periodic pings. Set load balancer and proxy idle timeouts above the Server's ping interval, with margin, and preserve bidirectional HTTP/2 streaming.

Size each Worker with this checklist. Defaults, caps, and metric names are in the [Server Workers reference](https://docs.superdurable.io/reference/server/workers#choose-slot-count).

- In containers and schedulers, set the slot count and the floor explicitly, because the CPU count can show the whole host and inflate derived values. The Server Workers reference lists the SDK defaults.
- I/O-bound handlers (model calls, HTTP, databases) need many slots, within the downstream rate limits.
- Python sync handlers share a thread pool, so CPU-bound Python work needs more processes.
- Size slots for the expected load, not for the Server's cap. Fixed and adaptive slot streams, and their defaults, are in the [Server Workers reference](https://docs.superdurable.io/reference/server/workers#adaptive-slot-streams).
- Scale Workers on the oldest age of queued Steps. A rising age with a low Server CPU means too few slots or Workers.
- The Server is limited by CPU. Move to a larger node when its CPU saturates.
- Past one node, split clusters by Flow type or tenant.

## Worker registration and readiness

- Start the Worker on its own goroutine, thread, or task when its start call blocks. Then call the SDK's bounded registration wait before serving traffic.
- The wait succeeds once the Worker's streams have registered with the Server. It proves registration, not free capacity. A busy Worker stays ready.
- Cancellation or timeout ends only the wait, not the Worker. Startup failures wake waiters.
- Use the readiness snapshot for later probes. After initial registration, transient disconnects do not flip readiness. Combine it with application lifecycle health, because a stopping Worker can still report ready.
- A permanent stream rejection (UNAUTHENTICATED, PERMISSION_DENIED, INVALID_ARGUMENT, UNIMPLEMENTED, or RESOURCE_EXHAUSTED) before readiness fails the wait with the SDK's pull registration error. By default the Worker keeps retrying rejected streams. An opt-in setting makes the Worker drain and stop on a permanent rejection that leaves no registered stream.
- Keep handling a blocking run failure after startup.

## Graceful shutdown

- On Stop, the Worker starts no new task. Idle streams close at once.
- A running handler finishes and sends its result before its stream closes. A task that arrives after shutdown begins is returned to the queue without consuming a retry.
- A handler still running at the drain deadline is canceled, and Dex fails that attempt as possibly run, which consumes a retry.
- A routine deployment whose handlers finish within the drain timeout therefore costs no retry. Keep handlers responsive to cancellation and let shutdown finish before the process exits.
- For a rolling deployment, start the new Workers and wait for their registration before stopping the old ones.
- Close the Client and the blob cache only after the Worker has drained. A draining handler may still call the shared Client, for example to start or call another Flow, and calls on a closed Client fail.

## Credentials and transport

- With Server authentication on, every Client and Worker call and every task stream carries a bearer credential. Clients and Workers have separate credential options.
- Use a rotating file credential or a current-token provider. A rotated token takes effect on the next call or stream reconnect.
- An API key credential exchanges a long-lived API key for short-lived access tokens at a token endpoint, for a named server ID. The SDK caches and refreshes the token, and retries an UNAUTHENTICATED call once with a fresh token. The token URL must be HTTPS, except loopback. Keep the API key in secret management.
- The Server API is plaintext gRPC. Terminate TLS in a gateway or load balancer that forwards gRPC over HTTP/2. Where the SDK has a FlowService TLS option, set it independently on the Client and the Worker. TLS never falls back to plaintext.
- Tokens and API keys never go in Flow state or rendered definitions; see [secrets](../../SKILL.md#attributes-loading-and-secrets).

Option names per language are in the [Developer reference](https://docs.superdurable.io/reference/) for the SDK, under Workers. Server authentication is in the [Server authentication reference](https://docs.superdurable.io/reference/server/authentication).

## Protocol compatibility

- Each Server and SDK declares an inclusive protocol interval. Worker startup and the Client's first call pick the highest common version.
- An SDK that needs a newer protocol fails Worker start or the first Client call with a clear error. Upgrade the Server first, then the SDKs.
- Running Workers do not renegotiate. A Server release that raises its minimum protocol requires stopping old Workers first.
- Run `dexcli version check` against the target Server before you deploy a Worker.
- Protocol compatibility says nothing about open Flows. Apply the versioning rules below separately.

Upgrade order and rollback rules are in the [Server upgrades reference](https://docs.superdurable.io/reference/server/upgrades).

## Versioning Flow code

Dex does not replay Flow handler code, so a deployment never causes a replay nondeterminism error. Open Flows can still meet changed business rules, missing Step implementations, renamed RPCs, or incompatible data. An open Flow is a durable contract between its recorded state and future Worker code; see [Must 4](../../SKILL.md#must).

Treat these names as data: Flow types, Step types, RPC names, Attribute, AttributeMap, Channel, ChannelMap, and Stream names, map instance keys, chunk sizes, partition counts, hash algorithms, and value schemas.

### Lock business behavior at Flow start

- When a rule change must not affect open Flows, persist a business version at start, in an Attribute or the start input, and branch on it. Do not branch on the Worker build: an open run is not pinned to one. Runs started before the version existed have none, so the code gives an absent version its legacy meaning. Deploy Workers that understand a new version value before new starts use it.
- Alternatively, start new runs on another task queue or Flow type and keep the old Worker pool for the old runs. A run's task queue is fixed at start.

### Evolve data and contracts additively

- Add a new Step type and route only new Flow runs to it.
- Add optional Attributes, Channels, Streams, or RPCs.
- Readers tolerate fields that older runs omit and ignore unknown fields from newer components. Keep the old meaning of existing values.
- For an incompatible payload or output, introduce a new Step type or RPC name.
- Settle persisted layouts before the first release. Changing them later means reading both forms for as long as old Flows or stored values exist.

### Keep old definitions until old Flows finish

- Keep old Step and RPC handlers while open Flow runs can reach them, through retries and any approved time travel.
- Before removing a definition, search for Flows that can still reach it, including recoverable terminal Flows. For example: `dexcli flow search --query 'FlowType = "OldFlowType"'`.
- During a rolling deployment that adds Step types or RPC names, start the new Workers first. Dex hands a Step only to a Worker that registers its Step type. A Step type that no connected Worker registers waits in the task queue without consuming an attempt, and an RPC name that no connected Worker registers fails. Old Workers still run every name they do register, so keep changed logic compatible.

### Steps that already wait

A deploy never changes the conditions of a Step that already waits. A Step that already waits keeps the conditions its finished `WaitFor` returned; the Worker that takes it runs the `Execute` of its deployed version. New `WaitFor` code applies only to Step executions that start after the deploy.

| Change for Steps that already wait | Do this |
| --- | --- |
| End a Timer earlier, such as a shorter deadline | New Steps get the new duration from the new `WaitFor`. Skip each old Timer with the Client `SkipTimer` once its new deadline passes. |
| Change what the Step waits for | Publish, from Flow code or an existing typed RPC, to a Channel the original Wait already includes. |
| Neither is enough | Only then cancel and restart the Step, with user authorization. |

To shorten deadlines, run a periodic job or an operator action:

1. Page through `SearchFlows` by Flow type and execution status to find candidate runs. Add `ActiveStepTypes` when the Flow config enables active-Step search.
2. For each run, read the business start time and the exact Step execution number from business state with the public Client: a registered Attribute, an AttributeMap instance with a known key, or an existing business record.
3. Call `SkipTimer` on each old Timer past its new deadline. Select it by the Condition ID or index of the old `WaitFor`.

- The search `StartTime` is the Flow run's start time, not the business start time.
- The Client Attribute read cannot enumerate an AttributeMap; read only keys you already know, or use a read-only RPC that loads the whole map.
- When the old relative duration is known, the new deadline is the old fire time from `dexcli flow state` minus the difference between the old and new durations.
- When an old run has no reliable record, a person must verify it before anyone skips its Timer.
- Skipping satisfies that Timer condition as if it fired; `Execute` is scheduled once the whole Wait is satisfied. The deadline branch needs no new code.
- This needs no new Step type, no new RPC, and no direct call to the generated Server API.

### Incompatible changes

Choose one explicit strategy: let old Flow runs finish on an old Worker pool, keep versioned handlers in one registry, migrate at a safe application boundary, or stop or recover selected executions with user authorization.

Upgrading the SDK and changing the Flow model are separate decisions. Test one Flow run started before the deployment and one started after it.

## Worker API availability

When a Step or RPC fails because the Worker API is unavailable:

1. Record the Flow ID, Run ID, Flow type, Worker version, and Server address.
2. Check Worker logs, then the network path: discovery, TLS, authentication, and load balancer.
3. Confirm the deployed Worker registers the Flow, Step, and RPC names the open Flow uses.
4. Inspect the Step in Dex Web: attempt, Worker error, gRPC status, and stack trace separate an unavailable Worker from an application failure.
5. Let an active retry continue if its budget is valid. Do not restart or time travel a Flow only to test connectivity.
6. For a terminal Flow, verify the fix with a new run first. Then time travel from a safe Step boundary only if that is the intended recovery.

## Self-check

- Do the Worker and the Client share one registry and payload configuration?
- Does the Worker wait for registration before serving RPCs or waits, and stop gracefully before exit?
- In containers, are the slot count and floor set explicitly?
- Are credentials and TLS configured independently on the Client and the Worker?
- Is business behavior locked at Flow start, with old definitions kept until old Flows finish?
