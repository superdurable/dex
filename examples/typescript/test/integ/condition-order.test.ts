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

import type { Flow } from "@superdurable/dex";

import {
  cancelCaseOrderProbeFlow,
  deferredChoiceOrderProbeFlow,
  type ConditionOrderOutcome,
} from "./condition-order-probe-flows.js";
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

async function startProbeFlow(flow: Flow<void>, prefix: string): Promise<string> {
  const flowId = environment.newFlowId(prefix);
  await environment.client.startFlow(flow, flowId, undefined, environment.startOptions());
  return flowId;
}

async function probeOutcomes(flowId: string, stepType: string): Promise<ConditionOrderOutcome[]> {
  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", `${flowId}: ${result.errorMessage ?? ""}`);
  return result.completions
    .filter((completion) => completion.stepType === stepType)
    .map((completion) => completion.decode<ConditionOrderOutcome>());
}

test("deferred choice: the declared-first response wins when both are pending", async () => {
  const flow = deferredChoiceOrderProbeFlow;
  const flowId = await startProbeFlow(flow, "pattern-deferred-choice-order");
  // Publish the later-declared response first: declaration order wins, not publish order.
  await environment.client.invokeRPC(flow.rejectProbeOffer, flowId, "no");
  await environment.client.invokeRPC(flow.acceptProbeOffer, flowId, "yes");
  await environment.client.invokeRPC(flow.openProbeGate, flowId);
  assert.deepEqual(await probeOutcomes(flowId, "AwaitOfferProbeResponse"), [
    { winner: "accepted", loserPendingCount: 1 },
  ]);
});

test("cancel case: the cancel wins when both are pending", async () => {
  const flow = cancelCaseOrderProbeFlow;
  const flowId = await startProbeFlow(flow, "pattern-cancel-case-order");
  await environment.client.invokeRPC(flow.decideProbeApplication, flowId, "approved");
  await environment.client.invokeRPC(flow.cancelProbeApplication, flowId, "applicant withdrew");
  await environment.client.invokeRPC(flow.openProbeGate, flowId);
  assert.deepEqual(await probeOutcomes(flowId, "AwaitProbeDecision"), [
    { winner: "canceled", loserPendingCount: 1 },
  ]);
});
