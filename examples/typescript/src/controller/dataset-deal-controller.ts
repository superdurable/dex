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
  DexError,
  ErrorSubStatus,
  InitialAttribute,
  StepExecutionId,
  type Client,
} from "@superdurable/dex";

import { startOptions } from "../config/env.js";
import { datasetDealFlow } from "../workflow/datasetdeal/dataset-deal-flow.js";
import {
  decodeDealProcess,
  decodeStateData,
  hasCondition,
  validateDealProcess,
} from "../workflow/datasetdeal/models.js";

const INITIALIZATION_TIMEOUT_MS = 30_000;

export function createDatasetDealRouter(client: Client): Router {
  const router = Router();

  router.post("/start", async (request, response) => {
    try {
      const body = requireRecord(request.body, "request body");
      const process = decodeDealProcess(body.process);
      validateDealProcess(process, datasetDealFlow.actions.availableNames());
      const buyerId = requireNonEmptyString(body.buyerId, "buyerId");
      const flowId = `${process.processId}-${randomUUID()}`;
      const runId = await client.startFlow(
        datasetDealFlow,
        flowId,
        process.processId,
        startOptions({
          attributes: [
            InitialAttribute.of(datasetDealFlow.buyerId, buyerId),
            InitialAttribute.of(datasetDealFlow.processDefinition, process),
          ],
          requestId: flowId,
        }),
      );
      await client.waitForStepCompletion(
        flowId,
        StepExecutionId.of(datasetDealFlow.initialize.getStepType()),
        INITIALIZATION_TIMEOUT_MS,
      );
      response.status(201).json({ flowID: flowId, runID: runId });
    } catch (failure) {
      if (failure instanceof TypeError) {
        response.status(400).json({ error: failure.message });
        return;
      }
      throw failure;
    }
  });

  router.post("/:flowId/trigger/:conditionName", async (request, response) => {
    const flowId = request.params.flowId;
    const conditionName = request.params.conditionName;
    let process;
    try {
      process = await client.getAttribute(flowId, datasetDealFlow.processDefinition);
    } catch (failure) {
      if (failure instanceof DexError && failure.subStatus === ErrorSubStatus.FLOW_NOT_EXISTS) {
        response.status(404).json({ error: `dataset deal ${flowId} was not found` });
        return;
      }
      throw failure;
    }
    if (process === undefined) {
      response.status(404).json({ error: `dataset deal ${flowId} was not found` });
      return;
    }
    if (!hasCondition(process, conditionName)) {
      response.status(400).json({ error: `condition ${conditionName} is not defined` });
      return;
    }
    try {
      const body = requireRecord(request.body, "request body");
      const data = decodeStateData(body.data);
      await client.publish(flowId, datasetDealFlow.conditionMessages, conditionName, data);
      response.status(202).json({ flowID: flowId, conditionName });
    } catch (failure) {
      if (failure instanceof TypeError) {
        response.status(400).json({ error: failure.message });
        return;
      }
      throw failure;
    }
  });

  return router;
}

function requireRecord(value: unknown, field: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new TypeError(`${field} must be an object`);
  }
  return value as Record<string, unknown>;
}

function requireNonEmptyString(value: unknown, field: string): string {
  if (typeof value !== "string" || value.trim().length === 0) {
    throw new TypeError(`${field} is required`);
  }
  return value.trim();
}
