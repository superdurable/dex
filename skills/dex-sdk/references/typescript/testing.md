# Testing TypeScript applications

Read [core testing](../core/testing.md) for the strategy, the scenario list,
and the local server settings. This page gives the TypeScript harness. The
block type-checks; `GreetingFlow` is the Flow from the
[handbook](typescript.md#wiring-skeleton).

## Harness

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

- Use `node:test` or the project's framework. Generate Flow IDs with
  `randomUUID()`.
- Give each environment its own `mkdtemp` BlobCache directory. Close the
  Client, the Worker, and then the BlobCache in `finally`.
- Bound every wait: `waitForFlow(flowId, timeoutMs)`, `requestTimeoutMs` on
  match waits, and a deadline on any polling helper.
- Assert typed failures with `assert.rejects(promise, ErrorClass)`.

## Worker replacement and crashes

- Replacement: close the Worker at a durable boundary, start a new `Worker`
  with the same Flow instances and a new BlobCache handle, then finish the
  Flow.
- `worker.close()` drains running handlers, so it cannot simulate a crash.
  Run the Worker in a child process (`child_process.spawn`) and kill it with
  `SIGKILL` mid-handler.

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
`./run-integration-tests.sh` starts and stops its own. Runnable harness:
[test/integ/environment.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/test/integ/environment.ts).
