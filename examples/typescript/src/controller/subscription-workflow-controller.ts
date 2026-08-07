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

import type { Client } from "@superdurable/dex";

import { startOptions } from "../config/env.js";
import type { Customer, Subscription } from "../workflow/subscription/models.js";
import { subscriptionFlow } from "../workflow/subscription/subscription-flow.js";

export function createSubscriptionRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (_request, response) => {
    const flowId = `subscription-${process.hrtime.bigint()}`;
    const subscription: Subscription = {
      trialPeriodMs: 20_000,
      billingPeriodMs: 10_000,
      maxBillingPeriods: 10,
      billingPeriodCharge: 100,
    };
    const customer: Customer = {
      firstName: "Quanzheng",
      lastName: "Long",
      id: "qlong",
      email: "qlong@example.com",
      subscription,
    };
    const runId = await client.startFlow(subscriptionFlow, flowId, customer, startOptions());
    response.json({ flowID: flowId, runID: runId });
  });

  router.get("/cancel", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.publish(workflowId, subscriptionFlow.cancelSubscription, undefined);
    response.json({});
  });

  router.get("/updateChargeAmount", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const newChargeAmount = Number(request.query.newChargeAmount ?? 0);
    await client.publish(workflowId, subscriptionFlow.updateChargeAmount, newChargeAmount);
    response.json({});
  });

  router.get("/describe", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const subscription = await client.invokeRPC(
      subscriptionFlow.describe,
      workflowId,
    );
    response.json(subscription);
  });

  return router;
}
