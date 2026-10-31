# Read-after-write consistency and asynchronous completion

Use this reference before implementing or reviewing sequential Client mutations and reads. Classify the **write API → read API pair**. Language-level `await`, a completed Future, or a nil error proves only that the selected API's response boundary was reached.

## Three different guarantees

1. **Acceptance:** Dex accepted the request. Acceptance alone does not mean the Server has processed it.
2. **Read-after-write:** after the write returns successfully, a subsequent read of the authoritative Flow state reflects its direct effects or a later state. The read may block until the Server catches up. Strong does not mean zero latency.
3. **Business completion:** Steps, provider operations, cleanup, and other work triggered by the write have finished. A fresh state read does not automatically establish this.

A successful RPC that writes Attributes, AttributeMap instances, or Channel messages has its direct effects applied before any later read of the same Flow is served. A typed read-only RPC issued after the write returns observes the write or a later state on its first successful read. So does a [Client Attribute read](primitives.md#read-attributes-from-outside-a-flow) of the written Attributes or exact map instances, which reads committed state without a Worker. Do not add callback waits or convergence polling to that pair.

This guarantee requires a successful write, a read initiated afterward, the same logical Flow and appropriate state selections, and no application failure preventing the effect from being applied. An AttributeMap instance must be loaded by the read RPC. Channel pending-message reads must load the queue; another consumer can remove a just-published message. Concurrent writes can supersede the value. Strong read-after-write does not add transaction isolation, make a multi-RPC sequence atomic, or guarantee a particular value survives competing mutations.

## Write/read matrix

| Write | Subsequent read or confirmation | Guarantee and what to do |
| --- | --- | --- |
| RPC that writes Attributes or AttributeMap instances | Typed read-only RPC over the same Attributes, loading the written map instances | **Strong.** The first successful read includes the write or a later state. Read once; do not poll. |
| RPC that writes Attributes or AttributeMap instances | Client Attribute read (Go **GetAttributes**, Python **get_attributes**, TypeScript **getAttributes**, Java **getAttributes**, Rust **get_attributes**) of the same Attributes or exact map instances | **Strong.** The read sees committed state only, so the first successful read includes the write or a later state. Read once; do not poll. It never includes writes that a running Step or RPC has staged but not committed. |
| RPC that publishes or deletes Channel or ChannelMap messages | Typed queue snapshot RPC that loads the Channel or ChannelMap | **Strong** for the pending queue. A consumed or deleted message is not expected to remain pending. A non-transactional deletion of a missing ID can be a no-op while the RPC's other effects apply. |
| Transactional or locked RPC | Typed read-only RPC over the written state | **Strong.** The RPC returns after its handler and effects commit together. It does not wait for triggered Steps or external projections. Requires an active Flow. |
| `UpdateFlowConfig` | Server or CLI Flow configuration read | **Strong** once the Flow can process the change. The write returns on acceptance. Applies only supplied fields and does not recall already-dispatched work. The public SDK has no generic configuration-read API. |
| `SkipTimer` | Current Timer and Step state; named Step completion or a business snapshot for the result | The Timer change is readable directly. **Execute and business completion are asynchronous.** The Timer may fire on its own before the skip applies; acceptance is not proof of execution. |
| `StopFlow` CANCEL/default or FAIL | `WaitForFlow` and explicit terminal status | **Closes the run as soon as it commits.** The stop does not wait for cleanup. A terminal result does not mean provider cleanup finished or the Attribute Store drained, because pending writes keep syncing afterward. CANCEL, FAIL and TERMINATE record different terminal statuses. |
| `StopFlow` TERMINATE | Terminal status | Also closes the run as soon as it commits. It does not prove provider compensation or projection completion. |
| Step or RPC write or delete of a hard-delete Attribute | Typed read RPC, another Step or RPC, or a retried attempt | **Visible before commit.** The stored value can reflect a write or deletion from an attempt that later failed, and concurrent writers may not leave the last committed write. Make handlers safe to repeat and avoid read-modify-write. See [hard-delete Attributes](data-handling.md#hard-delete-attributes-for-short-lived-secrets). |
| Any indexed Attribute write | `SearchFlows`, CLI search, or Dex Web work discovery | **Eventual.** A search miss is not proof the write failed. Confirm through the typed read RPC or a Client Attribute read, or bound a search retry when the caller needs the index itself. |
| Any Attribute Store-enabled write | External Attribute Store query | **Eventual latest-state projection.** Valid direct Flow state can be readable before the projection finishes. Projection failures do not roll back Flow state; retry exhaustion can prevent convergence, and so does a Flow ID that does not match the store's `rowKey` template, whose batches are skipped. |
| RPC Step movements, cancellation or close decisions | Named Step wait, Attribute-match or business condition, or terminal result | **Asynchronous business completion.** Cancellation can yield before later Step movements are queued; a direct Attribute snapshot is not a scheduling or completion barrier. |
| Server `SetAttributes`, `PublishToChannel`, or `DeleteChannelMessage` | Server Attribute or Channel read, or a typed application snapshot | Same direct-state guarantee as an RPC write. These Server write APIs are not public SDK APIs; applications write through typed RPCs. The Server Attribute read is public only as the Client Attribute read in every SDK. |
| SubFlow completion delivered to its parent | Parent's SubFlow condition, parent Step completion, or business state | Delivery is not proof that the parent consumed the result or completed subsequent work. It is internal, not a Client completion API. |

`StartFlow` and TimeTravel/Reset are not state writes. Their returned run identifies an accepted or new execution; use a named admission Step or Flow result when the API must establish business acceptance or completion. A Stream message is best-effort observation, not an authoritative receipt for any of the operations above.

## Lifecycle boundaries

A read after a write can block until the Server has processed the write. Set a read deadline that allows for that. Server unavailability can similarly block or fail the read. Do not weaken a strong path into polling to hide that failure. server-zd rejects a Flow start delay with INVALID_ARGUMENT, so a Flow never accepts a write before it starts processing.

From server-zd v0.1.8, a transactional or locking RPC on a closed run returns NOT_FOUND with FLOW_NOT_EXISTS before any Worker handler runs, and a retry with a committed request ID returns its recorded reply. Other RPCs still run on a closed, unpurged run, and their returned writes are rejected.

StopFlow closes the run as soon as it commits. A later write to that run returns NOT_FOUND except that, from server-zd v0.1.5, Server SetAttributes can erase currently stored hard-delete Attributes on a closed, unpurged run when every write sets one to null and the request sets no action permission mappings. For natural closes, server-zd v0.1.4 or later keeps the run RUNNING and `WaitForFlow` waiting until pending Attribute Store writes are written or dropped and previously admitted transactional or locking RPCs finish. A dropped write still needs reconciliation. v0.1.3 closed without waiting, so a table row could land after `WaitForFlow` returned. From server-zd v0.1.5, cleanup-sensitive requests rejected during Flow close cleanup carry FLOW_CLOSING. Use the SDK v1.20.0 or later typed error and read terminal Flow state before deciding whether to act again. See [server-zd](operations.md#server-zd).

The SDK UpdateFlowConfig call does not change a Flow's Worker routing; every Flow routes to pull Workers. An unavailable pull Worker is an availability failure, not evidence of eventual Attribute consistency.

Address a Flow by its FlowID; the RunID is diagnostic and must not become a business revision or fence. A query-only RPC and a Client Attribute read can read retained terminal state; locks, transactions, and returned effects require an active Flow. Retention expiry must remain an explicit missing or unavailable result.

## Confirmation and retries

Use a single typed authoritative read after a successful direct-state write. For subsequent business work, select the existing named Step completion, Attribute match, business RPC state, or terminal-result API that proves the requested condition. Bound the wait and inspect terminal status; a completed Flow proves success only if its completion contract guarantees that condition.

Never use a fixed sleep as confirmation or blindly resend accepted requests. Do not attach a callback to every write. Add an application acknowledgement only when its business contract needs a stronger completion fact that existing public observation cannot express. Do not invent an operation receipt or SDK config-wait API.

If transport failure or timeout makes a mutation's acceptance unknown, reconcile authoritative state before retrying an effect whose safe replay is not established. StartFlow follows the [start-first replay rule](error-handling.md#start-first-reconcile-only-after-an-error); a stable start identity and matching start options can permit bounded replay without a mandatory read. Preserve the request identity where the installed API supports it, and distinguish start deduplication from RPC idempotency. Config updates and timer skips do not acquire exactly-once semantics merely because the client retries.

Every SDK generates a fresh request ID for each RPC invocation and exposes no override. A retry through the SDK is therefore a new request and may run the RPC handler again. Make RPC handlers safe to repeat, or reconcile through a typed read RPC before retrying a write. From server-zd v0.1.10, concurrent transactional or locking RPCs to the same Flow run that share a request ID and carry identical payloads run the Worker handler once and receive the same result or error. A request that reuses the ID with a different payload fails with INVALID_ARGUMENT. This holds only while the shared in-flight task is active. The RPC timeout bounds only the Worker handler. If server-side commit processing exceeds its 60-second budget, the call returns UNAVAILABLE and the outcome is unknown. The server then releases the shared task, so another invocation with the same request ID can run the handler again while the first command may still commit. A retry after UNAVAILABLE, a timeout, or a node crash before commit may run the RPC again. An accepted command can still encounter closure or a processing failure; do not promise unconditional eventual success.

## Verification

For strong RPC read-after-write, test a real Server and Worker with repeated sequential writes, then invoke the typed read RPC immediately. Assert ordinary Attributes, loaded AttributeMap instances and non-consumed pending queues on the **first** successful read. No sleeps or convergence polling are allowed between that write and read. Cover ordinary, transactional, and locked RPC writes. Also assert the same state through the Client Attribute read on the first read.

Establish lifecycle transitions separately: close the Flow, then test retained query-only reads.

Use a controlled gate on a subsequent business Step or projection writer to prove direct state is readable while later work remains unfinished. Release the gate and confirm completion with a bounded condition. For a paused Server, distinguish accepted write submission from a read deadline, then resume and confirm state.

## Source authority

The following links explain the existing semantics at the bundle's immutable baselines. Preserve installed project versions and inspect version-matched source before writing exact API calls. This reference introduces no new API.

- [Server RPC routing](https://github.com/superdurable/dex/blob/server-zd/v0.1.13/server-zd/crates/dexzd-api/src/flow_service.rs)
- [Attribute writes](https://github.com/superdurable/dex/blob/server-zd/v0.1.13/server-zd/crates/dexzd-apply/src/command_handlers/client_writes.rs)
- [Attribute Store scheduling](https://github.com/superdurable/dex/blob/server-zd/v0.1.13/server-zd/crates/dexzd-engine/src/attribute_sync/mod.rs)
- [Go Client](https://github.com/superdurable/dex/blob/sdk-go/v1.30.0/sdk-go/dex/client.go), [Go Client Attribute read](https://github.com/superdurable/dex/blob/sdk-go/v1.30.0/sdk-go/dex/client_attributes.go), [Python Client](https://github.com/superdurable/dex/blob/sdk-python/v1.30.0/sdk-python/dex/client.py), [Python AsyncClient](https://github.com/superdurable/dex/blob/sdk-python/v1.30.0/sdk-python/dex/async_client.py), [Java Client](https://github.com/superdurable/dex/blob/sdk-java/v1.31.0/sdk-java/src/main/java/io/superdurable/dex/Client.java), [TypeScript Client](https://github.com/superdurable/dex/blob/sdk-typescript/v1.30.0/sdk-typescript/src/client.ts), [Rust Client](https://github.com/superdurable/dex/blob/sdk-rust/v1.31.0/sdk-rust/crates/dex-sdk/src/client.rs), [Rust Client Attribute read](https://github.com/superdurable/dex/blob/sdk-rust/v1.31.0/sdk-rust/crates/dex-sdk/src/client/attribute_reads.rs)
- [CLI operations and success fields](https://github.com/superdurable/dex/blob/cli-v1.30.0/cli/internal/command/flow_client_operations.go)
- [Web action RPC routing](https://github.com/superdurable/dex/blob/web/v1.30.0/web/api/v2.go)
