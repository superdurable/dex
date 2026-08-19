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

import { StepExecutionId, type Client } from "@superdurable/dex";

import { startOptions } from "../../config/env.js";
import type { OrderRequest } from "./models.js";
import type { OrderProcessingFlow } from "./order-processing-flow.js";

export function createOrderProcessingRouter(
  client: Client,
  flow: OrderProcessingFlow,
): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const flowId = `order-processing-${process.hrtime.bigint()}`;
    const input: OrderRequest = {
      orderId: flowId,
      email: "buyer@example.com",
      customerId: "customer-1",
      amount: 42,
      testFailAtShipping: String(request.query.testFailAtShipping ?? "") === "true",
    };
    const runId = await client.startFlow(flow, flowId, input, startOptions());
    await client.waitForStepCompletion(
      flowId,
      StepExecutionId.of("ChargeStep"),
      5 * 60 * 1000,
    );
    response.json({ flowID: flowId, runID: runId });
  });

  router.get("/approve", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const notes = String(request.query.notes ?? "");
    const output = await client.invokeRPC(flow.approve, workflowId, notes);
    response.json(output);
  });

  router.get("/describe", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const status = await client.invokeRPC(flow.describe, workflowId);
    response.json({ flowID: workflowId, status });
  });

  return router;
}
