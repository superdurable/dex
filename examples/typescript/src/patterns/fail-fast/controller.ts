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

import { startOptions } from "../../config/env.js";
import { payoutOutcome, type ExpenseReimbursementFlow } from "./expense-reimbursement-flow.js";

export function createFailFastRouter(client: Client, flow: ExpenseReimbursementFlow): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const amountCents = Number(request.query.amountCents);
    const bankAccount = String(request.query.bankAccount ?? "");
    if (!workflowId || !Number.isInteger(amountCents) || !bankAccount) {
      response.status(400).json({ error: "workflowId, amountCents, and bankAccount are required" });
      return;
    }
    const runId = await client.startFlow(flow, workflowId, { amountCents, bankAccount }, startOptions());
    response.send(runId);
  });

  router.get("/get-outcome", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const [outcome] = await client.getAttributes(workflowId, payoutOutcome);
    if (outcome === undefined) {
      response.status(404).json({ error: "the claim has no outcome yet" });
      return;
    }
    response.json(outcome);
  });

  return router;
}
