# Server (server-zd)

server-zd is the Dex Server. It stores every Flow's state and hands Steps and RPCs to Workers. Its only runtime dependency is object storage, such as Amazon S3 or an S3-compatible store.

Discuss Server internals only when the user operates or develops Dex itself. For an application, state Dex behavior and guarantees.

## Guarantees

- server-zd commits every write to object storage before it acknowledges it. The local store is a cache rebuilt from object storage after a restart.
- One active process serves a cluster. It holds a lease in object storage, so a second process cannot write at the same time. Standby processes take over when the active process stops or loses its lease.
- Workers and Clients connect to the Server's API port. The Server never calls in.
- The Server stores one Flow Definition Graph per Flow type, published by Workers or dexcli, for Dex Web to read. The graph never changes how Flows run. Replacement rules are in the [Flow definitions reference](https://docs.superdurable.io/reference/server/runtime-client#flow-definitions).
- The Server keeps a recent history window per run. History reads return only retained events. Never rebuild business state from history. Time travel works only inside the window, or to the beginning.
- Each run deduplicates a bounded window of recent request IDs. A very old request ID is treated as a new request. Do not rely on Dex to deduplicate unbounded retries of the same RPC.
- The Server enforces limits on value size, message size, per-run storage, Flow ID length, and concurrent waits. A request a limit rejects fails at once with RESOURCE_EXHAUSTED or INVALID_ARGUMENT. Keep large or growing data in bounded AttributeMap chunks or blobs. Some limits are recorded per run at start. Current values are in the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits).
- The Server rejects a Flow start delay and a Flow-level retry policy with INVALID_ARGUMENT. Wait inside the Flow with a Timer or Channel, and retry with Step retry options.

## Shutdown and failover

- On a graceful stop or a planned step down, the active process stops handing out Step tasks, keeps its lease while running attempts finish up to a drain timeout, then releases the lease.
- Steps not yet handed to a Worker do not fail; the next active process dispatches them.
- Attempts still running when the lease is released fail as possibly run and consume a retry.
- Client calls still open at a step down end with UNAVAILABLE. The outcome is unknown: the call may have committed, and an RPC handler may already have run.
- Retry reads and replay-safe operations with backoff, such as a StartFlow with the same Flow ID, Request ID, and options.
- Retry a mutation only when it is idempotent, or after you reconcile it through authoritative state, such as a typed read. An RPC retry is a new request and can run the handler again. See [error-handling.md](error-handling.md#ambiguous-mutations).
- On Kubernetes, set the termination grace period well above the drain timeout. See the [Kubernetes reference](https://docs.superdurable.io/reference/server/kubernetes#graceful-shutdown).

## Streams

The Stream Store keeps Stream messages in the active process's memory by default. A restart, a standby takeover, or a step down empties it. An operator can disable it; Client Stream calls then fail with FAILED_PRECONDITION and Step writes are dropped. Streams stay best effort in every configuration.

## Search backends

SearchFlows reads a search projection. The operator chooses the backend before production traffic:

| Backend | Needs | Note |
| --- | --- | --- |
| builtin | nothing | Scans local search documents within a per-query budget. Queries with ORDER BY scan more. |
| PostgreSQL | a PostgreSQL database | Caps index slots per type and the length of indexed Keyword values. |
| ParadeDB | PostgreSQL with the search extension | No fixed slot cap. |

- Search is eventually consistent for applications. An external backend waits briefly for its projection to catch up and returns UNAVAILABLE instead of stale results; retry with backoff.
- Plan application indexes with the generic slot pool in [attribute.md](attribute.md#indexed-attributes-and-search), sized for the backend.
- Slot registrations are permanent on a Server.

Details are in the [Server search reference](https://docs.superdurable.io/reference/server/search).

## Run it

| Mode | Command | Use |
| --- | --- | --- |
| Local development | `dexcli dev` | server-zd with embedded object storage, plus Dex Web |
| Production | `dex-server-zd serve --config FILE` | binaries and a container image; a StatefulSet on Kubernetes |
| One node on local disk | `dex-server-zd local` | embedded object store in its data directory, no standby |

- Dex Web is a separate process. See [web.md](web.md).
- Public API authentication is off by default. When enabled, every Client, Worker, Web, and CLI call carries a bearer credential. See [application.md](application.md#credentials-and-transport).
- The API is plaintext gRPC. Terminate TLS in front of it.
- Attribute Store sync is a Server feature that an Attribute opts into. See [attribute.md](attribute.md#attribute-store-sync).

Configuration, high availability, object storage, authentication, Worker dispatch, metrics, and upgrades are in the [Server reference](https://docs.superdurable.io/reference/server/). Read the version of the reference that matches the deployed Server.

## Self-check

- Does no application logic depend on history beyond the retained window?
- Does the design avoid start delays and Flow-level retries?
- Are large or growing values bounded so they stay within Server limits?
- Is the search backend chosen before production, with the index plan sized for it?
