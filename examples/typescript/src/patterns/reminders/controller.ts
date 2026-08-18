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
import { reminderFlow } from "./reminder-flow.js";

export function createRemindersRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (_request, response) => {
    const workflowId = `reminder_test_id_${process.hrtime.bigint()}`;
    await client.startFlow(
      reminderFlow,
      workflowId,
      undefined,
      startOptions(),
    );
    response.send(`started workflowId: ${workflowId}`);
  });

  router.get("/accept", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.invokeRPC(
      reminderFlow.accept,
      workflowId,
    );
    response.send("accepted");
  });

  router.get("/optout", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.publish(workflowId, reminderFlow.optOutReminder, undefined);
    response.send("done");
  });

  return router;
}
