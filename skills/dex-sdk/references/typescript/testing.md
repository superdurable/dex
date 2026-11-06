# Testing TypeScript applications

Read [core testing](../core/testing.md) for the strategy and the full scenario
list. This page gives the TypeScript harness.

Test durable behavior against a real Dex Server, such as `dexcli dev`. Mocks
cannot prove registration, codecs, durable waits, Worker replacement, retry
exhaustion, or terminal behavior. Unit tests may cover pure helpers and
codecs.

## Harness

Use Node's built-in test runner (`node:test`) or the project's framework.

```typescript
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import {
  Client, FlowAlreadyStartedError, IdReusePolicy, Registry, Worker, openBlobCache,
  stringCodec, type Flow,
} from "@superdurable/dex";

const serverAddress = process.env.DEX_FLOW_SERVICE_ADDRESS ?? "127.0.0.1:8801";

interface TestEnvironment {
  readonly client: Client;
  newFlowId(prefix: string): string;
}

async function withEnvironment(
  flows: readonly Flow<any>[],
  run: (environment: TestEnvironment) => Promise<void>,
): Promise<void> {
  const registry = new Registry(flows);
  const cacheDirectory = await mkdtemp(join(tmpdir(), "dex-test-"));
  const blobCache = openBlobCache({ directory: cacheDirectory, maxBytes: 64 << 20 });
  const worker = new Worker(registry, blobCache, { serverAddress });
  const client = new Client(registry, blobCache, { serverAddress });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
    await run({ client, newFlowId: (prefix) => `${prefix}-${randomUUID()}` });
  } finally {
    await client.close();
    await worker.close();
    blobCache.close();
    await rm(cacheDirectory, { recursive: true, force: true });
  }
}

test("greeting completes and rejects a duplicate Flow ID", async () => {
  const greetingFlow = new GreetingFlow();
  await withEnvironment([greetingFlow], async ({ client, newFlowId }) => {
    const flowId = newFlowId("greeting");
    const options = { idReusePolicy: IdReusePolicy.DISALLOW };
    await client.startFlow(greetingFlow, flowId, "Ada", options);
    const result = await client.waitForFlow(flowId, 30_000);
    assert.equal(result.status, "completed");
    assert.equal(result.singleOutput(stringCodec), "greeted Ada");
    await assert.rejects(client.startFlow(greetingFlow, flowId, "Ada", options), FlowAlreadyStartedError);
  });
});
```

Rules:

- Give every test a unique Flow ID (`randomUUID()`), and unique map instance
  and entity keys when tests share an Attribute Store.
- Give each test its own temporary BlobCache directory. Close the Client, the
  Worker, and then the BlobCache in `finally`.
- Bound every wait: `waitForFlow(flowId, timeoutMs)`, `requestTimeoutMs` on
  match waits, and a deadline on any polling helper. When the Client has a
  direct wait, use it instead of polling. Never use a fixed sleep as the
  assertion.
- On a timeout, report the Flow ID, run ID, status, and the last observed
  value.
- Assert business invariants, not the scheduling order of parallel Steps.
- For a fast local test server, follow the durability setting in
  [core testing](../core/testing.md).

## Worker replacement and crashes

- To prove resumption, stop at a durable boundary, close the Worker, start a
  new Worker with the same Flow definitions and a new BlobCache handle, and
  complete the Flow.
- `worker.close()` drains running handlers, so it cannot simulate a crash
  inside `execute`. Run the Worker in a child process
  (`child_process.spawn`) and kill it with `SIGKILL` mid-handler.
- Test retry exhaustion with a handler that fails deterministically, and
  assert the recovery Step's outcome or the terminal failure.

## Scenarios

1. Start, wait, and assert the output and terminal status.
2. Worker replacement while waiting, executing, and retrying.
3. Retry exhaustion and the configured recovery path.
4. Channel publish through an RPC before and after the wait registers; RPC
   idempotency; message deletion.
5. A Timer that fires across a Worker restart, without sleeping in the test.
6. Stream reads that resume with the returned token.
7. Graceful completion, force completion, failure, cancellation, timeout
   handler, and dead-end paths, each on its own.
8. Start identity: same, different, and generated request IDs, with
   `ignoreAlreadyStarted` on and off.
9. Post-terminal RPC, publish, and stop behavior with typed errors; a
   query-only Get on a closed Flow.
10. Heartbeat checkpoint recovery and cancellation through
    `cancellationSignal`.

## Commands

```bash
dexcli dev
cd examples/typescript
npm run typecheck
npm test
npm run test:integ
./run-integration-tests.sh
```

`npm run test:integ` expects a running Dex Server.
`./run-integration-tests.sh` manages the server itself. Runnable harness:
[test/integ/environment.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/test/integ/environment.ts).
