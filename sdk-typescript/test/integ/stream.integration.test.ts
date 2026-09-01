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

test("Stream messages retain duplicate sources and Step source metadata", async () => {
  const flow = new StreamTestFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("stream");
    await client.startFlow(flow, id, undefined);
    await client.waitForFlow(id, 30_000);

    await client.writeStream(id, flow.progress, "client#write", "client-progress");
    await client.writeStream(id, flow.progress, "client#write", "duplicate-source");

    let resumeToken = "";
    let stepSource = "";
    for (const expected of [
      "wait-progress-1",
      "wait-progress-2",
      "execute-progress-1",
      "execute-progress-2",
    ]) {
      const message = await client.readStream(id, flow.progress, resumeToken, 30_000);
      assert.equal(message.value, expected);
      assert.notEqual(message.resumeToken, resumeToken);
      assert.ok(message.createdTime.getTime() > 0);
      assert.ok(message.source.startsWith("#"));
      stepSource ||= message.source;
      assert.equal(message.source, stepSource);
      resumeToken = message.resumeToken;
    }

    const waitDetails = await client.readStream(id, flow.details, "", 30_000);
    const executeDetails = await client.readStream(
      id,
      flow.details,
      waitDetails.resumeToken,
      30_000,
    );
    assert.equal(waitDetails.value, "wait-details");
    assert.equal(executeDetails.value, "execute-details");
    assert.equal(waitDetails.source, stepSource);
    assert.equal(executeDetails.source, stepSource);

    const clientMessage = await client.readStream(id, flow.progress, resumeToken, 30_000);
    assert.equal(clientMessage.value, "client-progress");
    assert.notEqual(clientMessage.resumeToken, resumeToken);
    assert.ok(clientMessage.createdTime.getTime() > 0);
    assert.equal(clientMessage.source, "client#write");

    const duplicateSource = await client.readStream(
      id,
      flow.progress,
      clientMessage.resumeToken,
      30_000,
    );
    assert.equal(duplicateSource.value, "duplicate-source");
    assert.equal(duplicateSource.source, "client#write");
  });
});
