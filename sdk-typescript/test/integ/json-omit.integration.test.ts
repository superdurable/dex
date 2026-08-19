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
