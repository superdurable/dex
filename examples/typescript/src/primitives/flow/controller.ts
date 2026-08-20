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

import { Router } from "express";

import {
  FlowTimeoutPolicy,
  IdReusePolicy,
  InitialAttribute,
  type Client,
  type StartFlowOptions,
} from "@superdurable/dex";

import { startOptions } from "../../config/env.js";
import { exampleFlow, status } from "./example-flow.js";

function startFlowOptions(): StartFlowOptions {
  return startOptions({
    configOverride: {
      stepDurability: "sync",
    },
  });
}

export function exampleStartFlowOptions(): StartFlowOptions {
  return {
    timeoutMs: 30 * 60_000,
    timeoutPolicy: FlowTimeoutPolicy.FAIL,
    startDelayMs: 5 * 60_000,
    idReusePolicy: IdReusePolicy.DISALLOW,
    retryPolicy: {
      initialIntervalMs: 60_000,
      backoffCoefficient: 2,
      maximumIntervalMs: 10 * 60_000,
      maximumAttempts: 3,
    },
    attributes: [InitialAttribute.of(status, "queued")],
    configOverride: {
      stepDurability: "sync",
    },
    ignoreAlreadyStarted: true,
    requestId: "start-order-123",
  };
}

export async function rerouteActiveFlow(client: Client, flowId: string): Promise<void> {
  await client.updateFlowConfig(flowId, {
    workerTarget: { address: "worker-canary:8803" },
  });
}

export function createFlowRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const inputNum = Number(request.query.inputNum ?? "0");
    const runId = await client.startFlow(
      exampleFlow,
      workflowId,
      inputNum,
      startFlowOptions(),
    );
    response.json({ flowID: workflowId, runID: runId });
  });

  return router;
}
