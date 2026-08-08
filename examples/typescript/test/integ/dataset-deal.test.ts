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

import { InitialAttribute, StepExecutionId } from "@superdurable/dex";

import { DatasetDealAction } from "../../src/workflow/datasetdeal/actions.js";
import { comprehensiveDealProcess } from "../../src/workflow/datasetdeal/comprehensive-process.js";
import { stateDataCodec } from "../../src/workflow/datasetdeal/dataset-deal-flow.js";
import {
  acquireIntegEnvironment,
  awaitCondition,
  releaseIntegEnvironment,
} from "./environment.js";

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("dataset deal interprets the comprehensive process", async () => {
  const environment = await acquireIntegEnvironment();
  const flow = environment.datasetDealFlow;
  const process = comprehensiveDealProcess(environment.newFlowId("dataset-deal-process"));
  const flowId = environment.newFlowId(process.processId);
  const buyerId = environment.newFlowId("dataset-deal-buyer");

  const runId = await environment.client.startFlow(flow, flowId, process.processId, {
    ...environment.startOptions(),
    attributes: [
      InitialAttribute.of(flow.buyerId, buyerId),
      InitialAttribute.of(flow.processDefinition, process),
    ],
  });
  assert.ok(runId.length > 0);
  await environment.client.waitForStepCompletion(
    flowId,
    StepExecutionId.of(flow.initialize.getStepType()),
    30_000,
  );
  assert.deepEqual(await environment.client.getAttribute(flowId, flow.processDefinition), process);
  assert.equal(await environment.client.getAttribute(flowId, flow.buyerId), buyerId);

  await waitForAttribute(environment, flowId, flow.currentState, "buyer-negotiation");
  await environment.client.publish(flowId, flow.conditionMessages, "buyer-proposal", {
    proposedSamplePrice: "10",
    proposedFullPrice: "100",
    proposedSampleRefundPrice: "5",
  });
  await waitForAttribute(
    environment,
    flowId,
    flow.pendingPreConditionName,
    "seller-price-response",
  );
  await environment.client.publish(flowId, flow.conditionMessages, "seller-price-response", {
    acceptedProposedPrice: "false",
  });

  await waitForAttribute(environment, flowId, flow.currentState, "buyer-negotiation");
  await environment.client.publish(flowId, flow.conditionMessages, "buyer-proposal", {
    proposedSamplePrice: "11",
    proposedFullPrice: "105",
    proposedSampleRefundPrice: "5",
  });
  await waitForAttribute(
    environment,
    flowId,
    flow.pendingPreConditionName,
    "seller-price-response",
  );
  await environment.client.publish(flowId, flow.conditionMessages, "seller-price-response", {
    acceptedProposedPrice: "true",
  });
  await waitForAttribute(environment, flowId, flow.pendingPreConditionName, "sample-feedback");
  await environment.client.publish(flowId, flow.conditionMessages, "sample-feedback", {
    proceedToFullDataset: "true",
  });

  const output = await environment.client.waitForFlow(flowId, stateDataCodec, 45_000);
  assert.equal(output.deliveredDataset, "full");
  assert.equal(output.lastAction, DatasetDealAction.TRANSPORT_FULL_DATASET_TO_BUYER);
  assert.equal(output.lastActionStatus, "completed");
  assert.equal(
    await environment.client.getAttribute(flowId, flow.currentState),
    "process-full-order",
  );
});

async function waitForAttribute(
  environment: Awaited<ReturnType<typeof acquireIntegEnvironment>>,
  flowId: string,
  attribute: typeof environment.datasetDealFlow.currentState,
  expected: string,
): Promise<void> {
  await awaitCondition(
    () => environment.client.getAttribute(flowId, attribute),
    (value) => value === expected,
    15_000,
    `attribute ${attribute.name} did not become ${expected}`,
  );
}
