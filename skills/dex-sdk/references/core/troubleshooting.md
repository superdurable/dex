# Troubleshooting

Begin with evidence and keep diagnosis read-only. Capture the Flow ID, run ID, Flow type, SDK version, Dex Server address, Worker build, and the last user-visible operation.

Application failure policy belongs in pre-implementation design. Read [error handling](error-handling.md) before changing Client, RPC, cleanup, admission, wait, or Stream code; use this page to diagnose observed behavior.

## Diagnostic order

1. Check application and Worker logs for registration, handler, connectivity, codec, and blob errors.
2. Inspect the Flow in Dex Web. Compare the target, Flow type queue, captured dispatch sticky app Worker ID, serving app Worker ID, queue source, and dispatch failure reason; see [routing diagnostics](operations.md#dex-web-pull-routing-diagnostics).
3. Run `dexcli flow inspect <flow-id> --all-history` for a bounded history view.
4. Compare the active Step, Conditions, Attributes, pending Channels, Timers, and recent semantic events with the intended graph.
5. Reproduce the path with the smallest real Dex Server integration test.
6. Fix code or configuration before deciding whether an open execution needs an authorized recovery operation.

Use `dexcli flow search`, `summary`, `state`, and `history` for narrower JSON. Use `--no-hydrate` when payload contents are unnecessary or sensitive.

## Failure routing

- **Worker unavailable**: verify process health and the Worker's outbound connection to Dex Server. Inspect its outbound streams, app Worker ID, registered Flow types, Matching availability, and authentication. On server-zd v0.1.16 and earlier, an unavailable pull Step consumes a Step retry. On newer server-zd, it waits in the task queue instead.
- **Step stays pending**: on server-zd newer than v0.1.16, read the attempt's pending reason in Dex Web. **Waiting for a Worker** means no Worker of the Flow type took it: check Worker health, Step slot occupancy, **dexzd_step_task_queue_oldest_age_seconds**, and Worker scaling. **Waiting for a Worker that supports this Step type** means connected Workers lack the Step type: deploy a Worker that registers it, and check that an overridden app Worker ID changes after every restart. **Waiting for locks** means another Step or a transactional RPC holds one of its locks, or an earlier Step waits for one: find the holder and narrow lock scope. A Flow timeout still ends the wait; without one, the Step waits forever.
- **Registered pull Workers decline Steps**: only SDKs through Go, Java, Python, and TypeScript v1.32.0 and Rust v1.31.0 decline Steps for capacity. Compare busy handlers with **M − R**, reserve-decline counters, and RPC load. A reserve decline consumes no attempt, but on server-zd v0.1.16 and earlier **NO_WORKER_AVAILABLE** after the dispatch wait does. Newer server-zd returns legacy declines without consuming a retry. Size concurrency using the [reserve contract](pull-workers.md#reserved-rpc-capacity).
- **Unknown Flow, Step, RPC, or resource**: compare the Worker registry and persistence schema with the execution's stable names and deployed version.
- **Flow does not advance**: inspect the active Step's WaitFor conditions and pending Channel, Timer, or SubFlow state.
- **Repeated side effect**: inspect attempts and heartbeat checkpoints; make Execute idempotent or add compensation.
- **Failure after Worker replacement**: look for in-memory correctness state, incompatible codecs, unregistered old Step types, or changed schemas.
- **Large payload or hydration failure**: verify Client and Worker blob configuration, cache permissions, and matching codecs.
- **Lost progress**: determine whether the UI treated a best-effort Stream as durable state.
- **Stale queue mutation**: reload pending Channel messages; a consumed or deleted message ID cannot be reused.
- **Closed Flow interaction**: handle the language SDK's typed terminal/not-active result at the application boundary.

## Recovery boundary

Do not stop, time travel, publish, invoke, edit, delete, or skip a Timer during diagnosis. If the user authorizes recovery, read [operations.md](operations.md), resolve the current run, explain the expected change, perform one public operation, and re-inspect.

Read the selected language's **error-handling.md**, **observability.md**, and **gotchas.md** for concrete SDK behavior.
