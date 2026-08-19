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
import { moneyTransferFlow } from "./money-transfer-flow.js";
import type { TransferRequest } from "./transfer-request.js";

export function createMoneyTransferRouter(client: Client): Router {
  const router = Router();

  router.get("/start", async (request, response) => {
    const flowId = `money-transfer-${process.hrtime.bigint()}`;
    const transferRequest: TransferRequest = {
      fromAccount: String(request.query.fromAccount ?? ""),
      toAccount: String(request.query.toAccount ?? ""),
      amount: Number(request.query.amount ?? 0),
      notes: String(request.query.notes ?? ""),
    };
    const runId = await client.startFlow(
      moneyTransferFlow,
      flowId,
      transferRequest,
      startOptions(),
    );
    response.json({ flowID: flowId, runID: runId });
  });

  return router;
}
