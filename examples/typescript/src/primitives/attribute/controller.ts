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

import { FlowNotFoundError, type Client } from "@superdurable/dex";

import { startOptions } from "../../config/env.js";
import { attributeFlow } from "./attribute-flow.js";

export function createAttributeRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const message = String(request.query.message ?? "");
    const runId = await client.startFlow(attributeFlow, workflowId, message, startOptions());
    response.json({ flowID: workflowId, runID: runId });
  });

  router.get("/read", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    if (workflowId === "") {
      response.status(400).json({ error: "workflowId is required" });
      return;
    }
    try {
      const [status, paymentProgress, email] = await client.getAttributes(
        workflowId,
        attributeFlow.status,
        attributeFlow.progress.instance("payment"),
        attributeFlow.email,
      );
      response.json({
        status: status ?? "not started",
        paymentProgress: paymentProgress ?? "not started",
        // This Flow never sets email, so it reads as undefined.
        email: email ?? null,
      });
    } catch (error) {
      if (error instanceof FlowNotFoundError) {
        response.status(404).json({ error: error.message });
        return;
      }
      throw error;
    }
  });

  return router;
}
