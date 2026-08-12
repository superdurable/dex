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

import { stringCodec } from "../../src/index.js";
import {
  AsyncRpcFlow,
  AsyncStartAndWaitFlow,
  AsyncWaitForFlow,
} from "./async_handlers_flows.js";
import { flowId, withEnvironment } from "./environment.js";
import {
  MixedSyncAsyncStepsFlow,
  MixedSyncStepAsyncRpcFlow,
} from "./mixed_sync_async_flow.js";

// A synchronous Step could not await the child; that these pass on a single Worker
// proves the async handler yields the event loop so the same Worker serves the child.

test("async execute awaits Client.startFlow and Client.waitForFlow on one Worker", async () => {
  const parent = new AsyncStartAndWaitFlow();
  await withEnvironment([parent, parent.child], async ({ client }) => {
    parent.client = client;
    const parentId = flowId("async-parent");
    const childId = flowId("async-child");
    await client.startFlow(parent, parentId, childId);
    assert.equal(
      await client.waitForFlow(parentId, 30_000).then((result) => result.singleOutput(stringCodec)),
      `child-of-${childId}`,
    );
  });
});

test("async execute awaits Client.invokeRPC against a flow on the same Worker", async () => {
  const parent = new AsyncRpcFlow();
  await withEnvironment([parent, parent.target], async ({ client }) => {
    parent.client = client;
    const parentId = flowId("async-rpc-parent");
    const targetId = flowId("async-rpc-target");
    await client.startFlow(parent, parentId, targetId);
    assert.equal(await client.waitForFlow(parentId, 30_000).then((result) => result.singleOutput(stringCodec)), "rpc-echo:hello");
  });
});

test("Step waitFor may resolve a Wait asynchronously", async () => {
  const flow = new AsyncWaitForFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("async-waitfor");
    await client.startFlow(flow, id, "done");
    assert.equal(await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)), "done");
  });
});

test("sync and async Step handlers compose across movements", async () => {
  const flow = new MixedSyncAsyncStepsFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("mixed-sync-async-steps");
    await client.startFlow(flow, id, "mix");
    assert.equal(
      await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)),
      "mix-async-exec-sync-exec-done",
    );
  });
});

test("async RPC wakes a synchronous waiting Step on the same Worker", async () => {
  const flow = new MixedSyncStepAsyncRpcFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("mixed-sync-async-rpc");
    await client.startFlow(flow, id, "payload");
    assert.equal(await client.invokeRPC(flow.wake, id, "rpc"), "woke:rpc");
    assert.equal(await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)), "payload");
  });
});
