/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import assert from "node:assert/strict";
import test from "node:test";

import { StepExecutionId, stringCodec } from "@superdurable/dex";

import { recordBackfillFlow } from "../../src/patterns/control-long-running-work/record-backfill-flow.js";
import {
  acquireIntegEnvironment,
  releaseIntegEnvironment,
  type IntegEnvironment,
} from "./environment.js";

const WAIT_TIMEOUT_MS = 45_000;

let environment: IntegEnvironment;

test.before(async () => {
  environment = await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

async function startRecordBackfill(prefix: string): Promise<string> {
  const flowId = environment.newFlowId(prefix);
  await environment.client.startFlow(recordBackfillFlow, flowId, undefined, environment.startOptions());
  return flowId;
}

async function waitForFirstRecordBatch(flowId: string): Promise<void> {
  await environment.client.waitForStepCompletion(flowId, StepExecutionId.of("ProcessRecordBatch"), {
    requestTimeoutMs: WAIT_TIMEOUT_MS,
  });
}

async function completedOutput(flowId: string): Promise<string> {
  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", `${flowId}: ${result.errorMessage ?? ""}`);
  assert.equal(result.completions.length, 1);
  return result.completions[0]!.decode(stringCodec);
}

test("control long-running work reads the batch size each batch", async () => {
  // At the default batch size the backfill needs 100 seconds, longer than the wait timeout.
  const flowId = await startRecordBackfill("control-long-running-work-batch");

  for (const invalidSize of [0, 1001]) {
    await assert.rejects(
      environment.client.invokeRPC(recordBackfillFlow.setBatchSize, flowId, invalidSize),
      /batch size must be between 1 and 1000/,
    );
  }
  // The first batch has used the default size, so a later batch must read the new one.
  await waitForFirstRecordBatch(flowId);
  await environment.client.invokeRPC(recordBackfillFlow.setBatchSize, flowId, 1000);

  assert.equal(await completedOutput(flowId), "processed 1000 records");
});

test("control long-running work stops before the next batch", async () => {
  const flowId = await startRecordBackfill("control-long-running-work-stop");

  await waitForFirstRecordBatch(flowId);
  await environment.client.invokeRPC(recordBackfillFlow.requestStop, flowId);

  const output = await completedOutput(flowId);
  const match = /^stopped before record (\d+)$/.exec(output);
  assert.ok(match, output);
  assert.ok(Number(match[1]) >= 11, output);
});
