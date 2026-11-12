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

// Starts orders over HTTP: POST /orders with a JSON body such as {"orderId": "order-1"}.

import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";

import { FlowAlreadyStartedError, StepExecutionId, type Client } from "@superdurable/dex";

import type { OrderFlow } from "./order-flow.ts";

/** Starts the order's Flow once, however often the request is retried. Returns after Charge. */
export async function startOrder(client: Client, orderFlow: OrderFlow, orderId: string): Promise<void> {
  await client.startFlow(orderFlow, orderId, orderId, {
    requestId: `start-${orderId}`,
    ignoreAlreadyStarted: true,
  });
  await client.waitForStepCompletion(orderId, StepExecutionId.of("Charge"), {
    requestTimeoutMs: 30_000,
  });
}

export function createOrderServer(client: Client, orderFlow: OrderFlow): Server {
  return createServer((request, response) => {
    handleOrderRequest(client, orderFlow, request, response).catch((error: unknown) => {
      reply(response, 500, { error: String(error) });
    });
  });
}

async function handleOrderRequest(
  client: Client,
  orderFlow: OrderFlow,
  request: IncomingMessage,
  response: ServerResponse,
): Promise<void> {
  if (request.method !== "POST" || request.url !== "/orders") {
    reply(response, 404, { error: "not found" });
    return;
  }
  const chunks: Buffer[] = [];
  for await (const chunk of request) {
    chunks.push(chunk as Buffer);
  }
  const body = JSON.parse(Buffer.concat(chunks).toString("utf8")) as { orderId?: unknown };
  if (typeof body.orderId !== "string" || body.orderId === "") {
    reply(response, 400, { error: "orderId is required" });
    return;
  }
  try {
    await startOrder(client, orderFlow, body.orderId);
  } catch (error) {
    if (error instanceof FlowAlreadyStartedError) {
      reply(response, 409, { error: `order ${body.orderId} was already started` });
      return;
    }
    throw error;
  }
  reply(response, 200, { flowId: body.orderId, charged: true });
}

function reply(response: ServerResponse, status: number, payload: object): void {
  response.writeHead(status, { "Content-Type": "application/json" });
  response.end(JSON.stringify(payload));
}
