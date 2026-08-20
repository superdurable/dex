// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from "node:assert/strict";
import { credentials } from "@grpc/grpc-js";

import {
  FlowServiceClient,
  type GetFlowStateResponse,
  type StepMethodFailure,
} from "../../src/gen/dex.js";

function flowServiceClient(): InstanceType<typeof FlowServiceClient> {
  const serverAddress = process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801";
  return new FlowServiceClient(serverAddress, credentials.createInsecure());
}

function getFlowState(
  client: InstanceType<typeof FlowServiceClient>,
  flowId: string,
  runId: string,
): Promise<GetFlowStateResponse> {
  return new Promise((resolve, reject) => {
    client.getFlowState({ flowId, runId }, (error, response) => {
      if (error !== null && error !== undefined) {
        reject(error);
        return;
      }
      resolve(response ?? { activeStepExecutions: [] });
    });
  });
}

export async function awaitLiveWorkerFailure(
  flowId: string,
  runId: string,
  timeoutMilliseconds = 6_000,
): Promise<StepMethodFailure> {
  const client = flowServiceClient();
  const deadline = Date.now() + timeoutMilliseconds;
  while (Date.now() < deadline) {
    const response = await getFlowState(client, flowId, runId);
    for (const step of response.activeStepExecutions) {
      if (step.lastFailureInfo?.details !== undefined) {
        client.close();
        return step.lastFailureInfo;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  client.close();
  throw new Error("active Step did not expose retry failure");
}

export function assertWorkerFailureStackTrace(
  failure: StepMethodFailure,
  expectedDetail: string,
): void {
  assert.equal(failure.attempt, 1);
  assert.ok(failure.details !== undefined);
  assert.equal(failure.details.originalWorkerErrorDetail, expectedDetail);
  const stackTrace = failure.details.originalWorkerErrorStackTrace;
  assert.ok(stackTrace.length > 0);
  assert.ok(stackTrace.includes(expectedDetail));
}
