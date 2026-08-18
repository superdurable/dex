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

import { startOptions } from "../../../config/env.js";
import { isFlowMissingOrInactive } from "../../../service-errors.js";
import { drainSignalChannelsFlow } from "./drain-signal-channels-flow.js";

export function createDrainSignalRouter(client: Client): Router {
  const router = Router();

  router.get("/startorsignal", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    let message: string;
    try {
      await client.publish(
        workflowId,
        drainSignalChannelsFlow.queueSignalChannel,
        "signal from startorsignal endpoint",
      );
      message = "Signaled the workflow";
    } catch (error) {
      if (isFlowMissingOrInactive(error)) {
        const runId = await client.startFlow(
          drainSignalChannelsFlow,
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

  return router;
}
