// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { flowId, withEnvironment } from "./environment.js";
import { JsonOmitRpcFlow, JsonOmitStepFlow, type JsonOrder } from "./json_omit_codec_flow.js";

test("object Step input omits codec and completes with JSON", async () => {
  const flow = new JsonOmitStepFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("json-omit-step");
    await client.startFlow(flow, id, { orderId: "order-1" });
    const result = await client.waitForFlow(id, 30_000);
    assert.deepEqual(result.singleOutput<JsonOrder>(), { orderId: "order-1" });
  });
});

test("object RPC omits codecs and round-trips JSON", async () => {
  const flow = new JsonOmitRpcFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("json-omit-rpc");
    await client.startFlow(flow, id, undefined);
    const output = await client.invokeRPC(flow.describe, id, { orderId: "order-2" });
    assert.deepEqual(output, { orderId: "order-2" });
    await client.stopFlow(id);
  });
});
