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

import { setTimeout as delay } from "node:timers/promises";

import { Router, type Request, type Response } from "express";

import { RpcLockConflictError, type Client } from "@superdurable/dex";

import { OFFER_RESPONSE_WINDOW_CLOSED, offerResponseFlow } from "./offer-response-flow.js";

const RESPONSE_LOCK_CONFLICT_ATTEMPTS = 5;
const RESPONSE_LOCK_CONFLICT_BACKOFF_MS = 200;

type OfferResponseRPC = typeof offerResponseFlow.acceptCustomerOffer;

export function createDeferredChoiceRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const offerId = String(request.query.offerId ?? "");
    const runId = await client.startFlow(offerResponseFlow, workflowId, offerId, {});
    response.send(runId);
  });

  router.get("/accept", async (request, response) => {
    await sendCustomerResponse(client, offerResponseFlow.acceptCustomerOffer, request, response);
  });

  router.get("/reject", async (request, response) => {
    await sendCustomerResponse(client, offerResponseFlow.rejectCustomerOffer, request, response);
  });

  return router;
}

/** Answers 409 when the response window is already closed. */
async function sendCustomerResponse(
  client: Client,
  rpc: OfferResponseRPC,
  request: Request,
  response: Response,
): Promise<void> {
  const workflowId = String(request.query.workflowId ?? "");
  const note = String(request.query.note ?? "");
  const outcome = await invokeResponseRPC(client, rpc, workflowId, note);
  if (outcome === OFFER_RESPONSE_WINDOW_CLOSED) {
    response.status(409).send("offer response window closed");
    return;
  }
  response.send("offer response recorded");
}

/**
 * Retries a lock conflict with bounded backoff. Retrying is safe: a
 * conflicting call committed nothing.
 */
async function invokeResponseRPC(
  client: Client,
  rpc: OfferResponseRPC,
  workflowId: string,
  note: string,
): Promise<string> {
  for (let attempt = 1; ; attempt += 1) {
    try {
      return await client.invokeRPC(rpc, workflowId, note);
    } catch (error) {
      if (!(error instanceof RpcLockConflictError) || attempt === RESPONSE_LOCK_CONFLICT_ATTEMPTS) {
        throw error;
      }
      await delay(attempt * RESPONSE_LOCK_CONFLICT_BACKOFF_MS);
    }
  }
}
