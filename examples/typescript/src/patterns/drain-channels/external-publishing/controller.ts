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

/** Restarts a closed Flow ID; a retried start of this message returns its run. */
function externalPublishingStartOptions(messageId: string): StartFlowOptions {
  return startOptions({
    idReusePolicy: IdReusePolicy.ALLOW_IF_NOT_RUNNING,
    requestId: `start:${messageId}`,
    ignoreAlreadyStarted: true,
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
        await client.invokeRPCWithOptions(
          drainingExternalChannelFlow.publishExternalChannelMessage,
          workflowId,
          message,
          { requestId: `publish:${messageId}` },
        );
        response.send(`Accepted message ${messageId}`);
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
        response.send(
          `Accepted message ${messageId} as the start input of runId ${runId}`,
        );
        return;
      } catch (error) {
        // Another caller started a run first: publish this message to that run.
        if (!isFlowAlreadyStarted(error)) {
          throw error;
        }
      }
    }
    response
      .status(409)
      .send(
        `message ${messageId} is unconfirmed; send it again with the same messageId`,
      );
  });

  return router;
}
