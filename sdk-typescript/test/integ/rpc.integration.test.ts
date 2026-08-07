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
import { status } from "@grpc/grpc-js";

import {
  DexError,
  ErrorSubStatus,
  StopType,
  doubleCodec,
  type Client,
} from "../../src/index.js";
import { DeadEndFlow } from "./dead_end_flow.js";
import { expectError, flowId, withEnvironment } from "./environment.js";
import { NoStateFlow } from "./no_state_flow.js";
import { RpcFlow } from "./rpc_flow.js";

test("locking RPC serializes concurrent increments", async () => {
  const flow = new NoStateFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("rpc-lock");
    await client.startFlow(flow, id, undefined);
    const outcomes = await Promise.all(
      Array.from({ length: 100 }, async () => {
        try {
          await client.invokeRPC(flow.increaseCounter, id);
          return true;
        } catch (failure) {
          if (failure instanceof DexError && failure.code === status.ABORTED) {
            return false;
          }
          throw failure;
        }
      }),
    );
    const succeeded = outcomes.filter(Boolean).length;
    assert.ok(succeeded > 0);
    assert.equal(await client.invokeRPC(flow.getCounter, id), succeeded);
    await client.stopFlow(id);
  });
});

test("RPC without persistence wakes the waiting Step", async () => {
  const flow = new RpcFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("rpc-no-persistence");
    await client.startFlow(flow, id, 999);
    await client.invokeRPC(flow.noPersistence, id);
    assert.equal(await client.waitForFlow(id, doubleCodec, 30_000), 2);
  });
});

for (const suite of ["rpc", "rpc-memo"] as const) {
  test(`${suite} function with one argument`, async () => {
    const flow = new RpcFlow();
    await withEnvironment([flow], async ({ client }) => {
      const id = await startRpcFlow(client, flow, `${suite}-func-1`);
      await client.invokeRPC(flow.setData, id, "test-value");
      assert.equal(await client.invokeRPC(flow.getData, id), "test-value");
      await client.invokeRPC(flow.setData, id, undefined);
      assert.equal(await client.invokeRPC(flow.getData, id), undefined);
      await client.invokeRPC(flow.setKeyword, id, "test-value");
      assert.equal(await client.invokeRPC(flow.getKeyword, id), "test-value");
      await client.invokeRPC(flow.setKeyword, id, undefined);
      assert.equal(await client.invokeRPC(flow.getKeyword, id), undefined);
      assert.equal(await client.invokeRPC(flow.functionOne, id, "rpc-input"), RpcFlow.RPC_OUTPUT);
      await assertRpcCompletion(client, flow, id, "rpc-input");
    });
  });

  test(`${suite} function without arguments`, async () => {
    const flow = new RpcFlow();
    await withEnvironment([flow], async ({ client }) => {
      const id = await startRpcFlow(client, flow, `${suite}-func-0`);
      assert.equal(await client.invokeRPC(flow.functionZero, id), RpcFlow.RPC_OUTPUT);
      await assertRpcCompletion(client, flow, id, RpcFlow.HARDCODED_VALUE);
    });
  });

  test(`${suite} procedure with one argument`, async () => {
    const flow = new RpcFlow();
    await withEnvironment([flow], async ({ client }) => {
      const id = await startRpcFlow(client, flow, `${suite}-proc-1`);
      await client.invokeRPC(flow.procedureOne, id, "rpc-input");
      await assertRpcCompletion(client, flow, id, "rpc-input");
    });
  });

  test(`${suite} procedure without arguments`, async () => {
    const flow = new RpcFlow();
    await withEnvironment([flow], async ({ client }) => {
      const id = await startRpcFlow(client, flow, `${suite}-proc-0`);
      await client.invokeRPC(flow.procedureZero, id);
      await assertRpcCompletion(client, flow, id, RpcFlow.HARDCODED_VALUE);
    });
  });

  test(`${suite} read-only function`, async () => {
    const flow = new RpcFlow();
    await withEnvironment([flow], async ({ client }) => {
      const id = await startRpcFlow(client, flow, `${suite}-read-only`);
      assert.equal(await client.invokeRPC(flow.readOnly, id, "rpc-input"), RpcFlow.RPC_OUTPUT);
      await client.stopFlow(id, { type: StopType.FAIL, reason: RpcFlow.HARDCODED_VALUE });
    });
  });
}

test("RPC failure preserves structured worker details", async () => {
  const flow = new NoStateFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("rpc-error");
    await client.startFlow(flow, id, undefined);
    const failure = await expectError(
      client.invokeRPC(flow.fail, id, "this is an error"),
      DexError,
    );
    assert.equal(failure.code, status.FAILED_PRECONDITION);
    assert.equal(failure.subStatus, ErrorSubStatus.WORKER_API_ERROR);
    assert.match(failure.workerErrorType, /Error/);
    assert.match(failure.workerErrorDetail, /this is an error/);
    await client.stopFlow(id);
  });
});

test("RPC Context reports internal and external channel sizes", async () => {
  const flow = new DeadEndFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("channel-size");
    await client.startFlow(flow, id, undefined);
    assert.equal(await client.invokeRPC(flow.publishInternal, id), 1);
    assert.equal(await client.invokeRPC(flow.publishInternal, id), 2);
    await client.publish(id, flow.idleSignal, undefined, undefined, undefined);
    assert.equal(await client.invokeRPC(flow.signalSize, id), 3);
    await client.stopFlow(id);
  });
});

async function startRpcFlow(client: Client, flow: RpcFlow, prefix: string): Promise<string> {
  const id = flowId(prefix);
  await client.startFlow(flow, id, 999);
  return id;
}

async function assertRpcCompletion(
  client: Client,
  flow: RpcFlow,
  id: string,
  expectedValue: string,
): Promise<void> {
  assert.equal(await client.waitForFlow(id, doubleCodec, 30_000), 2);
  assert.equal(await client.getAttribute(id, flow.data), expectedValue);
  assert.equal(await client.getAttribute(id, flow.keyword), expectedValue);
  assert.equal(await client.getAttribute(id, flow.integer), RpcFlow.RPC_OUTPUT);
}
