// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import test from "node:test";

import { StopType } from "../../src/index.js";
import { flowId, withEnvironment } from "./environment.js";
import {
  GracefulCompletionConditionFlow,
  gracefulWaiterExecutions,
} from "./graceful_completion_condition_flow.js";

test("stopFlow GRACEFUL_COMPLETE wakes a Step waiting on the graceful completion condition", async () => {
  const flow = new GracefulCompletionConditionFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-graceful-condition-wake");
    await client.startFlow(flow, id, undefined);
    await client.invokeRPC(flow.openGate, id);
    await flow.waiterWaitForCalled;

    await client.stopFlow(id, { type: StopType.GRACEFUL_COMPLETE });

    const result = await client.waitForFlow(id, 15_000);
    assert.equal(result.status, "completed");
    const [executions] = await client.getAttributes(id, gracefulWaiterExecutions);
    assert.deepEqual(executions, [{ messages: [], isGracefulCompleted: true }]);
  });
});

test("a message published before the graceful stop is consumed first", async () => {
  const flow = new GracefulCompletionConditionFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-graceful-condition-message-first");
    await client.startFlow(flow, id, undefined);
    await client.invokeRPC(flow.publishMessage, id, "hello");
    await client.stopFlow(id, { type: StopType.GRACEFUL_COMPLETE });
    assert.equal((await client.describeFlow(id)).status, "running");

    await client.invokeRPC(flow.openGate, id);

    const result = await client.waitForFlow(id, 15_000);
    assert.equal(result.status, "completed");
    const [executions] = await client.getAttributes(id, gracefulWaiterExecutions);
    assert.deepEqual(executions, [
      { messages: ["hello"], isGracefulCompleted: true },
      { messages: [], isGracefulCompleted: true },
    ]);
  });
});
