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
import { StreamTestFlow } from "./stream_flow.js";

test("Stream messages round-trip through Dex with resume metadata and idempotency", async () => {
  const flow = new StreamTestFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("stream");
    const runId = await client.startFlow(flow, id, undefined);
    await client.waitForFlow(id, 30_000);

    await client.writeStream(id, flow.progress, "client-write", "client-progress");
    await client.writeStream(id, flow.progress, "client-write", "duplicate-ignored");

    const step = await client.readStream(id, flow.progress, "", 30_000);
    assert.equal(step.value, "step-progress");
    assert.notEqual(step.resumeToken, "");
    assert.ok(step.createdTime.getTime() > 0);
    assert.ok(step.idempotencyKey.startsWith(`${runId}#`));

    const clientMessage = await client.readStream(id, flow.progress, step.resumeToken, 30_000);
    assert.equal(clientMessage.value, "client-progress");
    assert.notEqual(clientMessage.resumeToken, step.resumeToken);
    assert.ok(clientMessage.createdTime.getTime() > 0);
    assert.equal(clientMessage.idempotencyKey, "client-write");
  });
});
