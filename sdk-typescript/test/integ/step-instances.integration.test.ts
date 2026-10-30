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

import { doubleCodec, stringCodec } from "../../src/index.js";

import { flowId, withEnvironment } from "./environment.js";
import { InstanceRecoveryFlow, StepInstancesFlow } from "./step-instances-flow.js";

test("generic Step instances route different input types by instance and StepRef", async () => {
  const flow = new StepInstancesFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("step-instances");
    await client.startFlow(flow, id, 0);
    assert.equal((await client.waitForFlow(id, 30_000)).singleOutput(doubleCodec), 2);
    assert.deepEqual(flow.observed, [0, "first", 1, "second", 2]);
  });
});

for (const reference of [false, true]) {
  test(`execute failure routes by ${reference ? "StepRef" : "instance"}`, async () => {
    const flow = new InstanceRecoveryFlow(reference);
    await withEnvironment([flow], async ({ client }) => {
      const id = flowId("instance-recovery");
      await client.startFlow(flow, id, undefined);
      assert.equal((await client.waitForFlow(id, 30_000)).singleOutput(stringCodec), "recovered");
    });
  });
}
