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

import type { Express, Router } from "express";
import { Router as createRouter } from "express";

import {
  FlowAlreadyStartedError,
  FlowNotActiveError,
  IdReusePolicy,
  StepExecutionId,
  type Client,
} from "@superdurable/dex";

import { startOptions } from "../../config/env.js";
import type { BackoffPollingFlow } from "../workflow/polling/backoff-polling-flow.js";
import type { SimplePollingFlow } from "../workflow/polling/simple-polling-flow.js";
import type { InterruptibleExecutionFlow } from "../workflow/interruptible/interruptible-execution-flow.js";
import type { ReminderFlow } from "../workflow/reminders/reminder-flow.js";
import type { StorageFlow } from "../workflow/storage/storage-flow.js";
import { StorageFlow as StorageFlowClass } from "../workflow/storage/storage-flow.js";
import type { AddStorageItemRequest } from "../workflow/storage/add-storage-item-request.js";
import type { ManualInterventionFlow } from "../workflow/intervention/manual-intervention-flow.js";
import type { ResettableTimerFlow } from "../workflow/resettabletimer/resettable-timer-flow.js";
import type { SimpleParallelStatesFlow } from "../workflow/parallel/simple-parallel-states-flow.js";
import type { ParallelStatesWithAwaitFlow } from "../workflow/parallel/parallel-states-with-await-flow.js";
import type { FailureRecoveryFlow } from "../workflow/recovery/failure-recovery-flow.js";
import type { RequestReceiverFlow } from "../workflow/scalableparallel/request-receiver-flow.js";
import type { ParentFlowV2 } from "../workflow/parentchild/parent-flow-v2.js";
import type { DrainInternalChannelsFlow } from "../workflow/drainchannels/internal/drain-internal-channels-flow.js";
import type { DrainSignalChannelsFlow } from "../workflow/drainchannels/signal/drain-signal-channels-flow.js";
import type { WaitForStateCompletionFlow } from "../workflow/waitforstatecompletion/wait-for-state-completion-flow.js";
import type { FlowGracefulTimeout } from "../workflow/timeout/flow-graceful-timeout.js";

export interface DesignPatternFlows {
  readonly simplePollingFlow: SimplePollingFlow;
  readonly backoffPollingFlow: BackoffPollingFlow;
  readonly interruptibleExecutionFlow: InterruptibleExecutionFlow;
  readonly reminderFlow: ReminderFlow;
  readonly storageFlow: StorageFlow;
  readonly manualInterventionFlow: ManualInterventionFlow;
  readonly resettableTimerFlow: ResettableTimerFlow;
  readonly simpleParallelStatesFlow: SimpleParallelStatesFlow;
  readonly parallelStatesWithAwaitFlow: ParallelStatesWithAwaitFlow;
  readonly failureRecoveryFlow: FailureRecoveryFlow;
  readonly requestReceiverFlow: RequestReceiverFlow;
  readonly parentFlowV2: ParentFlowV2;
  readonly drainInternalChannelsFlow: DrainInternalChannelsFlow;
  readonly drainSignalChannelsFlow: DrainSignalChannelsFlow;
  readonly waitForStateCompletionFlow: WaitForStateCompletionFlow;
  readonly flowGracefulTimeout: FlowGracefulTimeout;
}

export function registerDesignPatternRoutes(
  app: Express | Router,
  client: Client,
  flows: DesignPatternFlows,
): void {
  const router = createRouter();

  router.get("/polling/start/simple", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const runId = await client.startFlow(
      flows.simplePollingFlow,
      workflowId,
      undefined,
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/polling/start/backoff", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const runId = await client.startFlow(
      flows.backoffPollingFlow,
      workflowId,
      undefined,
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/interruptible/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const runId = await client.startFlow(
      flows.interruptibleExecutionFlow,
      workflowId,
      undefined,
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/interruptible/cancel", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.invokeRPC(
      flows.interruptibleExecutionFlow.interrupt,
      workflowId,
    );
    response.send("done");
  });

  router.get("/workflow-with-reminder/start", async (_request, response) => {
    const workflowId = `reminder_test_id_${process.hrtime.bigint()}`;
    await client.startFlow(
      flows.reminderFlow,
      workflowId,
      undefined,
      startOptions(),
    );
    response.send(`started workflowId: ${workflowId}`);
  });

  router.get("/workflow-with-reminder/accept", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.invokeRPC(
      flows.reminderFlow.accept,
      workflowId,
    );
    response.send("accepted");
  });

  router.get("/workflow-with-reminder/optout", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.publish(workflowId, flows.reminderFlow.optOutReminder, undefined);
    response.send("done");
  });

  router.post("/storage/add", async (request, response) => {
    const body = request.body as AddStorageItemRequest;
    await invokeStorageRpc(client, flows, (flowId) =>
      client.invokeRPC(
        flows.storageFlow.addItem,
        flowId,
        body,
      ),
    );
    response.send("Added storage item");
  });

  router.get("/storage/get", async (request, response) => {
    const itemKey = String(request.query.itemKey ?? "");
    const itemValue = await invokeStorageRpc(client, flows, (flowId) =>
      client.invokeRPC(
        flows.storageFlow.getItem,
        flowId,
        itemKey,
      ),
    );
    response.send(`Item: ${itemValue}`);
  });

  router.post("/storage/remove", async (request, response) => {
    const itemKey = String(request.query.itemKey ?? "");
    await invokeStorageRpc(client, flows, (flowId) =>
      client.invokeRPC(
        flows.storageFlow.removeItem,
        flowId,
        itemKey,
      ),
    );
    response.send("Removed storage item");
  });

  router.get("/intervention/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const runId = await client.startFlow(
      flows.manualInterventionFlow,
      workflowId,
      undefined,
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/resettabletimer/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const runId = await client.startFlow(
      flows.resettableTimerFlow,
      workflowId,
      undefined,
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/resettabletimer/reset", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.invokeRPC(
      flows.resettableTimerFlow.sendResetMessage,
      workflowId,
    );
    response.send("reset");
  });

  router.get("/parallel/start/simple", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const runId = await client.startFlow(
      flows.simpleParallelStatesFlow,
      workflowId,
      {
        id: "123",
        email: "jobseeker@indeed.com",
        phoneNumber: "0987654321",
      },
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/parallel/start/withAwait", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const countOfJobSeekers = Number(request.query.countOfJobSeekers ?? 50);
    const runId = await client.startFlow(
      flows.parallelStatesWithAwaitFlow,
      workflowId,
      countOfJobSeekers,
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/recovery/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const itemName = String(request.query.itemName ?? "");
    const quantity = Number(request.query.quantity ?? 0);
    await client.startFlow(
      flows.failureRecoveryFlow,
      workflowId,
      { itemName, requestedQuantity: quantity },
      startOptions(),
    );
    response.send("recovery workflow started");
  });

  router.get("/scalableparallel/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const numOfChildWfs = Number(request.query.numOfChildWfs ?? 0);
    await client.startFlow(flows.requestReceiverFlow, workflowId, numOfChildWfs, {
      ...startOptions(),
      idReusePolicy: IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED,
    });
    response.send("success");
  });

  router.get("/parentchild/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const numOfChildWfs = Number(request.query.numOfChildWfs ?? 0);
    await client.startFlow(flows.parentFlowV2, workflowId, numOfChildWfs, {
      ...startOptions(),
      idReusePolicy: IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED,
    });
    response.send("success");
  });

  router.get("/drainchannels/internal/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const runId = await client.startFlow(
      flows.drainInternalChannelsFlow,
      workflowId,
      "start-input",
      startOptions(),
    );
    response.send(runId);
  });

  router.get("/drainchannels/signal/startorsignal", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    let message: string;
    try {
      await client.publish(
        workflowId,
        flows.drainSignalChannelsFlow.queueSignalChannel,
        "signal from startorsignal endpoint",
      );
      message = "Signaled the workflow";
    } catch (error) {
      if (error instanceof FlowNotActiveError) {
        const runId = await client.startFlow(
          flows.drainSignalChannelsFlow,
          workflowId,
          "first message from start",
          startOptions(),
        );
        message = `Started the workflow with runId ${runId}`;
      } else {
        throw error;
      }
    }
    response.send(message);
  });

  router.get("/waitforstatecompletion/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.startFlow(
      flows.waitForStateCompletionFlow,
      workflowId,
      { id: 1, name: "Test Job Seeker", resume: "Test Resume", email: "testjobseeker@indeed.com" },
      startOptions(),
    );
    await client.waitForStepCompletion(
      workflowId,
      StepExecutionId.of("PersistData"),
      5 * 60 * 1000,
    );
    const persistedData = await client.invokeRPC(
      flows.waitForStateCompletionFlow.getJobSeekerData,
      workflowId,
    );
    response.send(
      `success for workflow ${workflowId} with data ${JSON.stringify(persistedData)}`,
    );
  });

  router.get("/timeout/start", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const successfulWorkflow = request.query.successfulWorkflow !== "false";
    await client.startFlow(
      flows.flowGracefulTimeout,
      workflowId,
      successfulWorkflow,
      startOptions(),
    );
    response.send(`success for workflow ${workflowId}`);
  });

  if (typeof (app as Express).listen === "function") {
    (app as Express).use("/design-pattern", router);
    return;
  }
  (app as Router).use(router);
}

async function invokeStorageRpc<T>(
  client: Client,
  flows: DesignPatternFlows,
  action: (flowId: string) => Promise<T>,
  attemptRestart = true,
): Promise<T> {
  const flowId = StorageFlowClass.getStorageFlowId();
  try {
    return await action(flowId);
  } catch (error) {
    if (!attemptRestart) {
      throw error;
    }
    const missing = error instanceof FlowNotActiveError;
    const message = error instanceof Error ? error.message : String(error);
    const staleWorker =
      /RPC ".*?" is not registered in flow "StorageFlow"/.test(message) ||
      /connect: connection refused/.test(message) ||
      /Error while dialing/.test(message) ||
      /Deadline exceeded/i.test(message) ||
      /timed out/i.test(message);
    if (!missing && !staleWorker) {
      throw error;
    }
    try {
      await client.stopFlow(flowId);
    } catch {
      // Flow may already be gone.
    }
    try {
      await client.startFlow(flows.storageFlow, flowId, undefined, startOptions());
    } catch (startError) {
      if (!(startError instanceof FlowAlreadyStartedError)) {
        throw startError;
      }
    }
    return invokeStorageRpc(client, flows, action, false);
  }
}
