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

import { randomUUID } from "node:crypto";

import { Router } from "express";

import {
  IdReusePolicy,
  type Client,
  type StartFlowOptions,
} from "@superdurable/dex";

import { startOptions } from "../../../config/env.js";
import {
  isFlowAlreadyStarted,
  isFlowMissingOrInactive,
} from "../../../service-errors.js";
import { drainingExternalChannelFlow } from "./draining-channel-flow.js";

/** Bounds how often a message that lost a start race is published again. */
const START_OR_PUBLISH_ATTEMPTS = 3;

/** Restarts the Flow ID once its run closed, with one start per message. */
function externalPublishingStartOptions(messageId: string): StartFlowOptions {
  return startOptions({
    idReusePolicy: IdReusePolicy.ALLOW_IF_NOT_RUNNING,
    requestId: `start:${messageId}`,
  });
}

export function createDrainingChannelRouter(client: Client): Router {
  const router = Router();

  router.get("/start-or-publish", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const messageId = String(request.query.messageId ?? "") || randomUUID();
    const message = `message ${messageId}`;
    for (let attempt = 0; attempt < START_OR_PUBLISH_ATTEMPTS; attempt++) {
      try {
        await client.invokeRPC(
          drainingExternalChannelFlow.publishExternalChannelMessage,
          workflowId,
          message,
        );
        response.send("Published to the Flow");
        return;
      } catch (error) {
        if (!isFlowMissingOrInactive(error)) {
          throw error;
        }
      }
      try {
        const runId = await client.startFlow(
          drainingExternalChannelFlow,
          workflowId,
          message,
          externalPublishingStartOptions(messageId),
        );
        response.send(`Started the workflow with runId ${runId}`);
        return;
      } catch (error) {
        // Another caller started the run first: publish this message to that run.
        if (!isFlowAlreadyStarted(error)) {
          throw error;
        }
      }
    }
    throw new Error(`message ${messageId} is unconfirmed: no run accepted it`);
  });

  return router;
}
