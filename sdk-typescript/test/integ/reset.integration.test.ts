// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from "node:assert/strict";
import test from "node:test";

import {
  FlowUncompletedError,
  ResetType,
  stringCodec,
  type Client,
} from "../../src/index.js";
import { expectError, flowId, withEnvironment } from "./environment.js";
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
        skipLockingRpcReapply: false,
        skipChannelMessagesReapply: false,
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
      const resetRunId = await client.resetFlow(id, {
        type: ResetType.BEGINNING,
        reason: "testing reset",
        skipLockingRpcReapply: true,
        skipChannelMessagesReapply: true,
      });
      const failure = await expectError(
        client.waitForFlow(id, stringCodec, 10_000),
        FlowUncompletedError,
      );
      assert.equal(failure.runId, resetRunId);
      assert.equal(failure.status, "timedOut");
      assert.equal(failure.resultCount, 0);
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
  assert.equal(await client.waitForFlow(id, stringCodec, 10_000), "lock complete");
  assert.equal((await client.describeFlow(id)).status, "completed");
  assert.equal(await client.getAttribute(id, flow.data), "random-string");
  assert.equal(await client.getAttribute(id, flow.keyword), "random-string");
  assert.equal(await client.getAttribute(id, flow.counter), 100);
}
