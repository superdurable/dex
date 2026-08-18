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
import { stepFlow } from "./step-flow.js";
import { retryFlow } from "./retry-flow.js";

export function createStepRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const inputNum = Number(request.query.inputNum ?? "0");
    const runId = await client.startFlow(stepFlow, workflowId, inputNum, startOptions());
    response.json({ flowID: workflowId, runID: runId });
  });

  router.get("/retry/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const readyAfterAttempt = Number(request.query.readyAfterAttempt ?? "0");
    const runId = await client.startFlow(
      retryFlow,
      workflowId,
      readyAfterAttempt,
      startOptions(),
    );
    response.json({ flowID: workflowId, runID: runId });
  });

  return router;
}
