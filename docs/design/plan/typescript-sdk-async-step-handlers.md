# TypeScript SDK: async Step / RPC handlers

Status: proposed
Audience: implement in a separate worktree (SDK change first, then simplify `examples/typescript`)

## Problem

Java examples call blocking `Client` APIs inside `Step.execute` (start child, wait for child, invoke RPC). That works because the Java Worker is multithreaded: one blocked execute thread does not stop other WorkerService RPCs.

TypeScript today:

1. `Step.execute` / `waitFor` / RPC methods must return synchronously (`StepDecision`, not `Promise`).
2. `Client` is fully async (`Promise`-based).
3. Examples bridge with `worker_threads` + `Atomics.wait` (`examples/typescript/src/patterns/client-sync.ts`) and a **second** gRPC Worker as sync `workerTarget` (`syncWorker` in `examples/typescript/src/main.ts`).

`Atomics.wait` freezes the Node event loop of the primary Worker. Child / RPC work started via the sync Client must target the sidecar Worker or the process deadlocks. That topology must not become the recommended app pattern.

## Goal

Let application code write idiomatic async Steps:

```ts
async execute(context: Context, uuid: number): Promise<StepDecision> {
  await client.startFlow(childFlow, childId, input, options);
  return goTo(this.awaitChildStep, { childWFId: childId, timerSeconds: 1 });
}
```

Same process, one Worker. No `Atomics.wait`, no sync sidecar Worker, no example-local `client-sync` bridge.

## Non-goals (this change)

- Durable “wait for child completion” as a first-class `Wait` condition (follow-up; see “Later”).
- Changing Java / Go / Python Step signatures.
- Shipping a public sync Client API based on `Atomics.wait`.

## Proposed SDK change

### API

Allow handlers to return a value or a Promise of that value:

| Handler | Today | Proposed |
|---------|--------|----------|
| `Step.execute` | `StepDecision` | `StepDecision \| Promise<StepDecision>` |
| `Step.waitFor` | `Wait` | `Wait \| Promise<Wait>` |
| RPC method body | sync result / `RPCResult` | same, or `Promise` of same |

Keep existing sync handlers valid (no migration required).

### Runtime

`WorkerDispatcher` already uses `async invokeExecute` / `invokeWaitFor` / `invokeRPC`. Await handler results:

```ts
const decision = await Promise.resolve(step.step.execute(context, input));
```

Same for `waitFor` and RPC invocation.

Because the handler is truly async (not `Atomics.wait`), the event loop stays free while `await client.*` is in flight. The same Worker can still serve child `invokeExecute` / RPCs. **One Worker is enough.**

### Caveats to document

- Long `await client.waitForFlow(...)` inside `execute` holds that WorkerService call until the poll finishes or times out. Prefer short timeouts + step retry (as parent–child examples already do with timer backoff), or later a durable Wait API.
- Do not use `Atomics.wait` / `child_process.spawnSync` inside Steps; they block the event loop and recreate the deadlock.
- Timeouts / retries on `executeMethodTimeoutMs` must still apply to the full async duration (confirm clocking in dispatcher / server options).

## Examples follow-up (after SDK release)

In `examples/typescript`:

1. Delete `src/patterns/client-sync.ts`, `src/patterns/dex-sync-worker.ts`.
2. Remove `syncWorker` / `syncBlobCache` / `DEX_SYNC_WORKER_BIND_ADDRESS` from `main.ts`, `config/env.ts`, `client-holder.ts`, README.
3. Replace `startFlowSync` / `waitForFlowSync` / `invokeRpcSync` / `isOptedInSync` with `await client.*` in async Steps:
   - `patterns/workflow/parentchild/parent-flow-v2.ts`
   - `patterns/workflow/scalableparallel/{parent-flow,request-receiver-flow}.ts`
   - `workflow/shortlistcandidates/{shortlist-flow,workflow-ids,is-opted-in-worker}.ts` (drop dedicated opt-in worker thread)
4. Keep `npm run smoke` green with a **single** Worker bind address.

Depend on a published or workspace `@superdurable/dex` that includes async handlers (bump examples package accordingly).

## Later (optional product improvement)

Even with async `execute`, polling `waitForFlow` from a Step is “Client as orchestrator”. A better Dex-shaped API:

- Wait condition for “flow X completed” (or child-complete channel helpers), similar to scalable-parallel’s completion channels.

Do not block the async-handler work on that.

## Reference: current workaround (to remove)

- Sync bridge: `examples/typescript/src/patterns/client-sync.ts` (`Atomics.wait` + `worker_threads`)
- Async Client thread: `examples/typescript/src/patterns/dex-sync-worker.ts`
- Sidecar Worker wiring: `examples/typescript/src/main.ts` (`syncWorker` + `setClient(client, syncWorker.workerTarget)`)
- Call sites: parent–child, scalable parallel, shortlist `isOptedInSync`

## Tests

SDK integ (`sdk-typescript/test/integ/`), against a running Dex:

1. **Step execute awaits Client.startFlow** — parent Step starts a child on the same Worker; child runs to completion; parent continues. Proves no dual-Worker requirement.
2. **Step execute awaits Client.waitForFlow** — parent waits for child with a short timeout then completes; covers Promise-returning `execute` + long poll without deadlock.
3. **Step execute awaits Client.invokeRPC** — flow A Step invokes RPC on flow B registered on the same Worker.
4. **Sync handlers still work** — existing integ suite unchanged (regression).
5. **waitFor returning Promise\<Wait\>** — if implemented in the same change; otherwise defer with a tracked follow-up test.

Examples (after cleanup):

6. **Smoke / integ** — `npm run test:integ` and `npm run smoke` with one Worker; parentchild + scalableparallel + shortlist paths must pass.

Do not add unit tests unless an edge case cannot be hit via integ.

## Documentation

- `sdk-typescript/README.md` — document that `execute` / `waitFor` / RPC may be `async` and may `await` `Client` on the same Worker; warn against blocking the event loop.
- This plan file — keep until shipped, then trim or move to changelog notes.
- `examples/typescript/README.md` — remove dual-Worker / `DEX_SYNC_WORKER_BIND_ADDRESS` once examples are simplified.
- Update any public TS API notes if `Step` / RPC typedefs are listed outside the README.

## UI/UX

N/A: no in-repo web UI.
