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
import { waitTypesFlow, type WaitTypesInput } from "./wait-types-flow.js";

export function createWaitTypesRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const mode = String(request.query.mode ?? "");
    const timeoutSeconds = Number(request.query.timeoutSeconds ?? 60);
    const input: WaitTypesInput = { mode, timeoutSeconds };
    const runId = await client.startFlow(waitTypesFlow, workflowId, input, startOptions());
    response.json({ flowID: workflowId, runID: runId });
  });

  router.get("/signal-a", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.invokeRPC(waitTypesFlow.signalA, workflowId);
    response.send("done");
  });

  router.get("/signal-b", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.invokeRPC(waitTypesFlow.signalB, workflowId);
    response.send("done");
  });

  return router;
}
