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
import {
  TASK_A_COMPLETED,
  TASK_B_COMPLETED,
  pollingFlow,
  taskACompleted,
  taskBCompleted,
} from "./polling-flow.js";

export function createPollingRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const pollingCompletionThreshold = Number(request.query.pollingCompletionThreshold ?? 0);
    const runId = await client.startFlow(
      pollingFlow,
      workflowId,
      pollingCompletionThreshold,
      startOptions(),
    );
    response.json({ flowID: workflowId, runID: runId });
  });

  router.get("/complete", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const channel = String(request.query.channel ?? "");
    if (channel === TASK_A_COMPLETED) {
      await client.publish(workflowId, taskACompleted, undefined);
      response.json({});
      return;
    }
    if (channel === TASK_B_COMPLETED) {
      await client.publish(workflowId, taskBCompleted, undefined);
      response.json({});
      return;
    }
    response.status(400).json({ error: "channel must identify task A or task B" });
  });

  return router;
}
