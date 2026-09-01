/*
 * Copyright (c) 2026 Super Durable, Inc.
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
import { progress, streamFlow } from "./stream-flow.js";

export function createStreamRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const input = String(request.query.input ?? "");
    const runId = await client.startFlow(streamFlow, workflowId, input, startOptions());
    response.json({ flowID: workflowId, runID: runId });
  });

  router.get("/write", async (request, response) => {
    await client.writeStream(
      String(request.query.workflowId ?? ""),
      progress,
      String(request.query.source ?? ""),
      String(request.query.message ?? ""),
    );
    response.send("done");
  });

  router.get("/read", async (request, response) => {
    const message = await client.readStream(
      String(request.query.workflowId ?? ""),
      progress,
      String(request.query.resumeToken ?? ""),
      20_000,
    );
    response.json(message);
  });

  return router;
}
