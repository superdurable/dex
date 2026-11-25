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
import { recordBackfillFlow } from "./record-backfill-flow.js";

export function createControlLongRunningWorkRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const runId = await client.startFlow(
      recordBackfillFlow,
      workflowId,
      undefined,
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/set-batch-size", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const size = Number(request.query.batchSize);
    if (!Number.isInteger(size)) {
      response.status(400).json({ error: "batchSize must be an integer" });
      return;
    }
    await client.invokeRPC(recordBackfillFlow.setBatchSize, workflowId, size);
    response.send("done");
  });

  router.get("/stop", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.invokeRPC(recordBackfillFlow.requestStop, workflowId);
    response.send("done");
  });

  return router;
}
