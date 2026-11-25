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

import {
  CURRENT_TRANSCRIPT_CHUNK,
  supportTicketFlow,
  transcriptChunks,
} from "./support-ticket-flow.js";

export function createInboundEventLoopRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    // No Flow timeout: the 24-hour inactivity Timer closes a quiet ticket.
    const runId = await client.startFlow(supportTicketFlow, workflowId, "", {});
    response.send(runId);
  });

  router.get("/reply", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const text = String(request.query.text ?? "");
    await client.invokeRPC(supportTicketFlow.sendTicketReply, workflowId, text);
    response.send("reply accepted");
  });

  router.get("/resolve", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const reason = String(request.query.reason ?? "");
    await client.invokeRPC(supportTicketFlow.resolveTicket, workflowId, reason);
    response.send("resolution accepted");
  });

  router.get("/transcript", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const [chunk] = await client.getAttributes(
      workflowId,
      transcriptChunks.instance(CURRENT_TRANSCRIPT_CHUNK),
    );
    response.json({ replies: chunk?.replies ?? [] });
  });

  return router;
}
