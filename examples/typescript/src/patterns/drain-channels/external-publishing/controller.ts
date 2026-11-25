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

/**
 * Restarts a closed Flow ID. A retried start returns the original run while that run is still
 * the latest run for the Flow ID and its start request ID matches.
 */
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
        // A different start request owns the active run: publish this message to that run.
        if (!isFlowAlreadyStarted(error)) {
          throw error;
        }
      }
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
        // That run closed before the publish: start again.
        if (!isFlowMissingOrInactive(error)) {
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
