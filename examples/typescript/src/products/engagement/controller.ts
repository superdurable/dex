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
import { engagementFlow } from "./engagement-flow.js";
import type { EngagementInput } from "./models.js";

export function createEngagementRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (_request, response) => {
    const flowId = `engagement-${process.hrtime.bigint()}`;
    const input: EngagementInput = {
      employerId: "test-employer-id",
      jobSeekerId: "test-job-seeker-id",
      notes: "test-notes",
    };
    const runId = await client.startFlow(engagementFlow, flowId, input, startOptions());
    response.json({ flowID: flowId, runID: runId });
  });

  router.get("/describe", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const description = await client.invokeRPC(
      engagementFlow.describe,
      workflowId,
    );
    response.json(description);
  });

  router.get("/optout", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.publish(workflowId, engagementFlow.optOutReminder, undefined);
    response.json({});
  });

  router.get("/decline", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const notes = String(request.query.notes ?? "");
    const status = await client.invokeRPC(
      engagementFlow.decline,
      workflowId,
      notes,
    );
    response.json(status);
  });

  router.get("/accept", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const notes = String(request.query.notes ?? "");
    const status = await client.invokeRPC(
      engagementFlow.accept,
      workflowId,
      notes,
    );
    response.json(status);
  });

  return router;
}
