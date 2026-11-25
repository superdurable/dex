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

import {
  FlowClosingError,
  FlowNotActiveOrNotFoundError,
  FlowNotFoundError,
  RpcLockConflictError,
  type Client,
} from "@superdurable/dex";

import { startOptions } from "../../config/env.js";
import {
  OFFER_RESPONSE_WINDOW_CLOSED,
  offerResponseFlow,
} from "./offer-response-flow.js";

const RESPONSE_LOCK_CONFLICT_ATTEMPTS = 5;
const RESPONSE_LOCK_CONFLICT_BACKOFF_MS = 200;

type OfferResponseRPC = typeof offerResponseFlow.acceptCustomerOffer;

export function createDeferredChoiceRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const offerId = String(request.query.offerId ?? "");
    if (!workflowId || !offerId) {
      response
        .status(400)
        .json({ error: "workflowId and offerId are required" });
      return;
    }
    const runId = await client.startFlow(
      offerResponseFlow,
      workflowId,
      offerId,
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/accept", async (request, response) => {
    await sendCustomerResponse(
      client,
      offerResponseFlow.acceptCustomerOffer,
      request,
      response,
    );
  });

  router.get("/reject", async (request, response) => {
    await sendCustomerResponse(
      client,
      offerResponseFlow.rejectCustomerOffer,
      request,
      response,
    );
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
  if (!workflowId || !note) {
    response.status(400).json({ error: "workflowId and note are required" });
    return;
  }
  let outcome: string;
  try {
    outcome = await invokeResponseRPC(client, rpc, workflowId, note);
  } catch (error) {
    const state = await classifyInactiveFlow(client, error, workflowId);
    if (state === "missing") {
      response.status(404).send("offer response flow not found");
      return;
    }
    if (state !== "closed") {
      throw error;
    }
    outcome = OFFER_RESPONSE_WINDOW_CLOSED;
  }
  if (outcome === OFFER_RESPONSE_WINDOW_CLOSED) {
    response.status(409).send("offer response window closed");
    return;
  }
  response.send("offer response recorded");
}

/**
 * Tells a Flow that is closing or closed from one that never existed.
 *
 * @returns "closed" when the Flow is closing or closed, "missing" when it never
 *   existed, and "other" when the error is not about an inactive Flow.
 */
async function classifyInactiveFlow(
  client: Client,
  error: unknown,
  workflowId: string,
): Promise<"closed" | "missing" | "other"> {
  // DescribeFlow may still report running while the Flow closes.
  if (error instanceof FlowClosingError) {
    return "closed";
  }
  if (!(
    error instanceof FlowNotActiveOrNotFoundError ||
    error instanceof FlowNotFoundError
  )) {
    return "other";
  }
  try {
    const flow = await client.describeFlow(workflowId);
    return flow.status === "running" ? "other" : "closed";
  } catch (describeError) {
    if (
      describeError instanceof FlowNotFoundError ||
      describeError instanceof FlowNotActiveOrNotFoundError
    ) {
      return "missing";
    }
    throw describeError;
  }
}

/**
 * Retries a lock conflict with bounded backoff. Retrying is safe: a
 * conflicting call committed nothing.
 *
 * @returns The RPC outcome.
 * @throws {RpcLockConflictError} When every attempt conflicts.
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
      if (
        !(error instanceof RpcLockConflictError) ||
        attempt === RESPONSE_LOCK_CONFLICT_ATTEMPTS
      ) {
        throw error;
      }
      await delay(attempt * RESPONSE_LOCK_CONFLICT_BACKOFF_MS);
    }
  }
}
