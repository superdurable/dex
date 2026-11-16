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

import { StopType, stringCodec, type FlowResult } from "../../src/index.js";
import { flowId, withEnvironment } from "./environment.js";
import { StopCompleteFlow } from "./stop_complete_flow.js";

const STOP_OUTPUT = "completed by StopFlow";

test("stopFlow FORCE_COMPLETE cancels the active Step and completes with the output", async () => {
  const flow = new StopCompleteFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-force-complete");
    await client.startFlow(flow, id, 1);
    await flow.executeStarted;

    await client.stopFlow(id, { type: StopType.FORCE_COMPLETE, output: STOP_OUTPUT });

    const result = await client.waitForFlow(id, 15_000);
    flow.releaseExecute();
    assertOnlyStopOutput(result);
  });
});

test("stopFlow GRACEFUL_COMPLETE waits for the active Step", async () => {
  const flow = new StopCompleteFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-graceful-complete");
    await client.startFlow(flow, id, 1);
    await flow.executeStarted;

    await client.stopFlow(id, { type: StopType.GRACEFUL_COMPLETE, output: STOP_OUTPUT });

    assert.equal((await client.describeFlow(id)).status, "running");
    flow.releaseExecute();
    assertOnlyStopOutput(await client.waitForFlow(id, 15_000));
  });
});

test("stopFlow rejects an output with CANCEL before sending", async () => {
  const flow = new StopCompleteFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-cancel-output");
    await client.startFlow(flow, id, 1);
    await flow.executeStarted;

    await assert.rejects(
      client.stopFlow(id, { type: StopType.CANCEL, output: STOP_OUTPUT }),
      (failure: unknown) =>
        failure instanceof TypeError && failure.message.includes("StopFlowOptions.output requires"),
    );

    assert.equal((await client.describeFlow(id)).status, "running");
    flow.releaseExecute();
  });
});

function assertOnlyStopOutput(result: FlowResult): void {
  assert.equal(result.status, "completed");
  assert.equal(result.completions.length, 1);
  const completion = result.completions[0]!;
  assert.equal(completion.stepType, "");
  assert.equal(completion.stepExecutionId, "");
  assert.equal(completion.decode(stringCodec), STOP_OUTPUT);
}
