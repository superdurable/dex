// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import test from "node:test";

import {
  ResetType,
  stringCodec,
  type Client,
} from "../../src/index.js";
import { flowId, withEnvironment } from "./environment.js";
import { RpcLockingFlow } from "./rpc_locking_flow.js";

for (const locking of [true, false]) {
  test(`reset reapplies ${locking ? "locking RPC" : "channel RPC"}`, async () => {
    const flow = new RpcLockingFlow();
    await withEnvironment([flow], async ({ client }) => {
      const id = await startAndInvoke(client, flow, locking);
      await assertCompletedWithAttributes(client, flow, id);
      const resetRunId = await client.resetFlow(id, {
        type: ResetType.BEGINNING,
        reason: "testing reset",
        skipWritesReapply: false,
      });
      await assertCompletedWithAttributes(client, flow, id);
      assert.equal((await client.describeFlow(id)).runId, resetRunId);
    });
  });

  test(`reset can skip ${locking ? "locking RPC" : "channel RPC"} reapply`, async () => {
    const flow = new RpcLockingFlow();
    await withEnvironment([flow], async ({ client }) => {
      const id = await startAndInvoke(client, flow, locking);
      await assertCompletedWithAttributes(client, flow, id);
      await client.resetFlow(id, {
        type: ResetType.BEGINNING,
        reason: "testing reset",
        skipWritesReapply: true,
      });
      const failure = await client.waitForFlow(id, 10_000);
      assert.equal(failure.status, "failed");
      assert.equal(failure.completions.length, 0);
      assert.equal(await client.getAttribute(id, flow.data), undefined);
      assert.equal(await client.getAttribute(id, flow.keyword), undefined);
      assert.equal(await client.getAttribute(id, flow.counter), undefined);
    });
  });
}

async function startAndInvoke(
  client: Client,
  flow: RpcLockingFlow,
  locking: boolean,
): Promise<string> {
  const id = flowId("reset");
  await client.startFlow(flow, id, undefined, { timeoutMs: 3_000 });
  await client.invokeRPC(locking ? flow.withLocking : flow.withoutLocking, id);
  return id;
}

async function assertCompletedWithAttributes(
  client: Client,
  flow: RpcLockingFlow,
  id: string,
): Promise<void> {
  assert.equal(await client.waitForFlow(id, 10_000).then((result) => result.singleOutput(stringCodec)), "lock complete");
  assert.equal((await client.describeFlow(id)).status, "completed");
  assert.equal(await client.getAttribute(id, flow.data), "random-string");
  assert.equal(await client.getAttribute(id, flow.keyword), "random-string");
  assert.equal(await client.getAttribute(id, flow.counter), 100);
}
