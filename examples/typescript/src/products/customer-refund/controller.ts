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

import { Router, type ErrorRequestHandler } from "express";

import {
  FlowAlreadyStartedError,
  FlowClosingError,
  FlowNotActiveOrNotFoundError,
  FlowNotFoundError,
  RequestTimeoutError,
  RpcLockConflictError,
  StepExecutionId,
  WorkerInvocationError,
  type Client,
  type FlowStatus,
} from "@superdurable/dex";

import { startOptions } from "../../config/env.js";
import {
  REFUND_NOT_AWAITING_MANAGER_ERROR_TYPE,
  caseStatus,
  refundId,
  type CustomerRefundFlow,
  type DecisionReason,
} from "./refund-flow.js";
import { REFUND_SCENARIOS, type RefundScenario } from "./refund-settings.js";

export function createCustomerRefundRouter(client: Client, flow: CustomerRefundFlow): Router {
  const router = Router();

  router.get("/manager-approval/start", async (request, response) => {
    const flowId = flowIdFromQuery(request.query) || `customer-refund-${process.hrtime.bigint()}`;
    const scenario = String(request.query.scenario || "normal");
    if (!Object.hasOwn(REFUND_SCENARIOS, scenario)) {
      const scenarios = Object.keys(REFUND_SCENARIOS).join(", ");
      response.status(400).json({ error: `scenario must be one of ${scenarios}` });
      return;
    }
    try {
      const input = REFUND_SCENARIOS[scenario as RefundScenario];
      const runId = await client.startFlow(flow, flowId, input, startOptions());
      try {
        await client.waitForStepCompletion(flowId, StepExecutionId.of("CheckPolicy"), {
          requestTimeoutMs: 10_000,
        });
      } catch (error) {
        // A policy denial or a small refund can finish before the wait attaches.
        if (!(error instanceof FlowNotActiveOrNotFoundError || error instanceof FlowClosingError)) {
          throw error;
        }
      }
      response.json({ flowID: flowId, runID: runId });
    } catch (error) {
      if (error instanceof FlowAlreadyStartedError) {
        response.status(409).json({ error: "this refund already exists" });
        return;
      }
      if (error instanceof RequestTimeoutError) {
        response.status(504).json({ error: `refund ${flowId} did not complete its policy check in time` });
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
      response.status(400).json({ error: "workflowId is required" });
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
      const run = await client.describeFlow(flowId);
      if (run.flowType !== flow.getFlowType() || run.status !== "running") {
        response.status(404).json({ error: `refund ${flowId} is closed or does not exist` });
        return;
      }
      await client.invokeRPC(rpc, flowId, input);
      response.json({ flowID: flowId, decision });
    } catch (error) {
      if (error instanceof FlowNotActiveOrNotFoundError || error instanceof FlowNotFoundError) {
        response.status(404).json({ error: `refund ${flowId} is closed or does not exist` });
        return;
      }
      if (error instanceof RpcLockConflictError) {
        response.status(409).json({ error: "another decision for this refund is in progress" });
        return;
      }
      if (error instanceof FlowClosingError) {
        response.status(409).json({ error: "this refund is closing" });
        return;
      }
      if (error instanceof WorkerInvocationError && error.workerErrorType === REFUND_NOT_AWAITING_MANAGER_ERROR_TYPE) {
        response.status(409).json({ error: "this refund is not waiting for a manager" });
        return;
      }
      throw error;
    }
  });

  router.get("/manager-approval/describe", async (request, response) => {
    const flowId = flowIdFromQuery(request.query);
    if (flowId === "") {
      response.status(400).json({ error: "workflowId is required" });
      return;
    }
    try {
      const run = await client.describeFlow(flowId);
      if (run.flowType !== flow.getFlowType()) {
        response.status(404).json({ error: `refund ${flowId} does not exist` });
        return;
      }
      const [status, issuedRefundId] = await client.getAttributes(flowId, caseStatus, refundId);
      response.json({
        flowID: flowId,
        status: flowStatusName(run.status),
        caseStatus: status ?? null,
        refundId: issuedRefundId || null,
      });
    } catch (error) {
      if (error instanceof FlowNotFoundError) {
        response.status(404).json({ error: error.message });
        return;
      }
      throw error;
    }
  });

  router.use(sendRefundError);

  return router;
}

export function flowStatusName(status: FlowStatus): string {
  switch (status) {
    case "running":
    case "completed":
    case "failed":
    case "terminated":
      return status;
    case "cancelled":
      return "canceled";
    case "serverSideTimeoutInternalOnly":
      return "timeout";
    default:
      return "unknown";
  }
}

const sendRefundError: ErrorRequestHandler = (error: unknown, _request, response, _next) => {
  const message = error instanceof Error ? error.message : String(error);
  response.status(500).json({ error: message });
};

/** Reads the Flow ID from flowId, or from its alias workflowId. */
function flowIdFromQuery(query: Record<string, unknown>): string {
  return String(query.workflowId || query.flowId || "");
}
