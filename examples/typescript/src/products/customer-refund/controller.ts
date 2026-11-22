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

import {
  FlowAlreadyStartedError,
  FlowNotActiveOrNotFoundError,
  FlowNotFoundError,
  RpcLockConflictError,
  WorkerInvocationError,
  type Client,
} from "@superdurable/dex";

import { startOptions } from "../../config/env.js";
import { caseStatus, refundId, type CustomerRefundFlow, type DecisionReason } from "./refund-flow.js";
import { REFUND_SCENARIOS, type RefundScenario } from "./refund-settings.js";

export function createCustomerRefundRouter(client: Client, flow: CustomerRefundFlow): Router {
  const router = Router();

  router.get("/manager-approval/start", async (request, response) => {
    const flowId = flowIdFromQuery(request.query) || `customer-refund-${process.hrtime.bigint()}`;
    const scenario = String(request.query.scenario ?? "normal");
    if (!Object.hasOwn(REFUND_SCENARIOS, scenario)) {
      const scenarios = Object.keys(REFUND_SCENARIOS).join(", ");
      response.status(400).json({ error: `scenario must be one of ${scenarios}` });
      return;
    }
    try {
      const input = REFUND_SCENARIOS[scenario as RefundScenario];
      const runId = await client.startFlow(flow, flowId, input, startOptions());
      response.json({ flowId, runId, scenario });
    } catch (error) {
      if (error instanceof FlowAlreadyStartedError) {
        response.status(409).json({ error: `Flow ID ${flowId} is already used; choose another one` });
        return;
      }
      throw error;
    }
  });

  router.get("/manager-approval/decide", async (request, response) => {
    const flowId = flowIdFromQuery(request.query);
    const decision = String(request.query.decision ?? "");
    const reason = String(request.query.reason ?? "").trim();
    if (flowId === "") {
      response.status(400).json({ error: "flowId is required" });
      return;
    }
    if (decision !== "approve" && decision !== "reject") {
      response.status(400).json({ error: "decision must be approve or reject" });
      return;
    }
    if (reason === "") {
      response.status(400).json({ error: "a reason is required" });
      return;
    }
    const rpc = decision === "approve" ? flow.approveRefund : flow.rejectRefund;
    const input: DecisionReason = { reason };
    try {
      await client.invokeRPC(rpc, flowId, input);
      response.json({ flowId, decision });
    } catch (error) {
      if (error instanceof FlowNotActiveOrNotFoundError) {
        response.status(404).json({ error: `refund ${flowId} is closed or does not exist` });
        return;
      }
      if (error instanceof RpcLockConflictError) {
        response.status(409).json({ error: `another decision on refund ${flowId} is being processed` });
        return;
      }
      // The RPC refuses a run that is not waiting for a manager.
      if (error instanceof WorkerInvocationError) {
        response.status(409).json({ error: error.message });
        return;
      }
      throw error;
    }
  });

  router.get("/manager-approval/describe", async (request, response) => {
    const flowId = flowIdFromQuery(request.query);
    if (flowId === "") {
      response.status(400).json({ error: "flowId is required" });
      return;
    }
    try {
      const run = await client.describeFlow(flowId);
      const [status, issuedRefundId] = await client.getAttributes(flowId, caseStatus, refundId);
      response.json({
        flowId,
        runId: run.runId,
        status: run.status,
        "case-status": status ?? null,
        "refund-id": issuedRefundId ?? null,
      });
    } catch (error) {
      if (error instanceof FlowNotFoundError) {
        response.status(404).json({ error: error.message });
        return;
      }
      throw error;
    }
  });

  return router;
}

/** Reads the Flow ID from flowId, or from its alias workflowId. */
function flowIdFromQuery(query: Record<string, unknown>): string {
  return String(query.flowId ?? query.workflowId ?? "").trim();
}
