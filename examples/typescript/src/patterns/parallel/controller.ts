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
import { parallelStatesWithAwaitFlow } from "./parallel-states-with-await-flow.js";
import { simpleParallelStatesFlow } from "./simple-parallel-states-flow.js";

export function createParallelRouter(client: Client): Router {
  const router = Router();

  router.get("/start/simple", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const runId = await client.startFlow(
      simpleParallelStatesFlow,
      workflowId,
      {
        id: "123",
        email: "jobseeker@indeed.com",
        phoneNumber: "0987654321",
      },
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/start/withAwait", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const countOfJobSeekers = Number(request.query.countOfJobSeekers ?? 50);
    const runId = await client.startFlow(
      parallelStatesWithAwaitFlow,
      workflowId,
      countOfJobSeekers,
      startOptions(),
    );
    response.send(runId);
  });

  return router;
}
