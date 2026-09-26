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

import { InitialAttribute, type Client } from "@superdurable/dex";

import {
  CHUNKED_SUBSCRIBER_FLOW_ID,
  CURRENT_CHUNK_INSTANCE,
  chunkedSubscriberFlow,
  validateSubscriberPageToken,
  type RegisterSubscriberInput,
} from "./chunked-subscriber-flow.js";

export function createSequentiallyChunkedAttributeMapRouter(client: Client): Router {
  const router = Router();

  router.post("/start", async (_request, response) => {
    const runId = await client.startFlow(
      chunkedSubscriberFlow,
      CHUNKED_SUBSCRIBER_FLOW_ID,
      undefined,
      {
        ignoreAlreadyStarted: true,
        attributes: [
          InitialAttribute.of(chunkedSubscriberFlow.archiveState, {
            nextSequence: 1,
            latestArchivedChunkToken: "",
          }),
          InitialAttribute.mapValue(
            chunkedSubscriberFlow.subscriberChunks,
            CURRENT_CHUNK_INSTANCE,
            { subscribers: [] },
          ),
        ],
      },
    );
    response.json({ flowID: CHUNKED_SUBSCRIBER_FLOW_ID, runID: runId });
  });

  router.post("/register", async (request, response) => {
    const body = request.body as Partial<RegisterSubscriberInput>;
    const input = {
      subscriberId: requireNonemptyString(body.subscriberId, "subscriberId"),
      deliveryAddress: requireNonemptyString(body.deliveryAddress, "deliveryAddress"),
    };
    response.json(
      await client.invokeRPC(
        chunkedSubscriberFlow.registerSubscriber,
        CHUNKED_SUBSCRIBER_FLOW_ID,
        input,
      ),
    );
  });

  router.get("/subscribers", async (request, response) => {
    const pageToken = validateSubscriberPageToken(
      typeof request.query.pageToken === "string" ? request.query.pageToken : "",
    );
    response.json(
      await client.invokeRPCWithOptions(
        chunkedSubscriberFlow.getSubscriberPage,
        CHUNKED_SUBSCRIBER_FLOW_ID,
        { pageToken },
        {
          loadAttributeMapInstances: [
            chunkedSubscriberFlow.subscriberChunks.load(pageToken),
          ],
        },
      ),
    );
  });

  return router;
}

function requireNonemptyString(value: unknown, name: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`${name} is required`);
  }
  return value;
}
