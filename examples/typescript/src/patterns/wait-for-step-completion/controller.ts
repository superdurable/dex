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
import { waitForStepCompletionFlow } from "./wait-for-step-completion-flow.js";

export function createWaitForStepCompletionRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.startFlow(
      waitForStepCompletionFlow,
      workflowId,
      { id: 1, name: "Test Job Seeker", resume: "Test Resume", email: "testjobseeker@indeed.com" },
      startOptions(),
    );
    await client.waitForStepCompletion(
      workflowId,
      StepExecutionId.of("PersistData"),
      15_000,
    );
    const persistedData = await client.invokeRPC(
      waitForStepCompletionFlow.getJobSeekerData,
      workflowId,
    );
    response.send(
      `success for workflow ${workflowId} with data ${JSON.stringify(persistedData)}`,
    );
  });

  return router;
}
