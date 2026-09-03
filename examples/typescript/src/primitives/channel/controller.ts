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
import { channelFlow, queued } from "./channel-flow.js";

export function createChannelRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const inputNum = Number(request.query.inputNum ?? "0");
    const runId = await client.startFlow(channelFlow, workflowId, inputNum, startOptions());
    response.json({ flowID: workflowId, runID: runId });
  });

  router.get("/approve", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.invokeRPC(channelFlow.approve, workflowId);
    response.send("done");
  });

  router.get("/enqueue", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const value = String(request.query.value ?? "");
    await client.publish(workflowId, queued, value);
    response.send("done");
  });

  router.get("/messages", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    response.json(await client.getChannelMessages(workflowId, queued));
  });

  router.get("/delete", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const messageId = String(request.query.messageId ?? "");
    await client.deleteChannelMessage(workflowId, queued, messageId);
    response.send("done");
  });

  router.get("/move", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const messageId = String(request.query.messageId ?? "");
    await client.invokeRPC(channelFlow.move, workflowId, { messageId });
    response.send("done");
  });

  return router;
}
