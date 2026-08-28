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

import type { Client, Flow } from "@superdurable/dex";

import { startOptions } from "../../config/env.js";
import {
  awaitParallelStepsFlow,
  dynamicParallelStepsFlow,
  firstWinParallelStepsFlow,
  staticParallelStepsFlow,
} from "./parallel-step-flows.js";

export function createParallelRouter(client: Client): Router {
  const router = Router();

  router.get("/start/static", async (request, response) => {
    response.send(await start(client, staticParallelStepsFlow, request, "work"));
  });
  router.get("/start/dynamic", async (request, response) => {
    response.send(await start(client, dynamicParallelStepsFlow, request, 3));
  });
  router.get("/start/await", async (request, response) => {
    response.send(await start(client, awaitParallelStepsFlow, request, 3));
  });
  router.get("/start/first-win", async (request, response) => {
    response.send(await start(client, firstWinParallelStepsFlow, request, 3));
  });

  return router;
}

async function start<T>(
  client: Client,
  flow: Flow<T>,
  request: { query: Record<string, unknown> },
  input: T,
): Promise<string> {
  return client.startFlow(
    flow,
    String(request.query.workflowId ?? ""),
    input,
    startOptions(),
  );
}
