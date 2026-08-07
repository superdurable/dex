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

import { DAY_MS } from "../config/env.js";
import type { JobInfo } from "../workflow/jobpost/job-info.js";
import { jobPostFlow } from "../workflow/jobpost/job-post-flow.js";

function escapeQuote(input: string): string {
  let value = input;
  if (value.startsWith("'")) {
    value = value.substring(1, value.length - 1);
  }
  if (value.startsWith('"')) {
    value = value.substring(1, value.length - 1);
  }
  return value;
}

export function createJobPostRouter(client: Client): Router {
  const router = Router();

  router.get("/create", async (request, response) => {
    const flowId = `job_id_${Math.floor(Date.now() / 1000)}`;
    const title = escapeQuote(String(request.query.title ?? ""));
    const description = escapeQuote(String(request.query.description ?? ""));
    await client.startFlow(jobPostFlow, flowId, undefined, {
      timeoutMs: DAY_MS,
      attributes: [
        InitialAttribute.of(jobPostFlow.title, title),
        InitialAttribute.of(jobPostFlow.jobDescription, description),
        InitialAttribute.of(jobPostFlow.lastUpdateTimeMillis, BigInt(Date.now())),
      ],
      configOverride: {
        continueAsNewThreshold: 10,
      },
    });
    response.send(`started workflowId: ${flowId}`);
  });

  router.get("/read", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const jobInfo = await client.invokeRPC(jobPostFlow.get, workflowId);
    response.json(jobInfo);
  });

  router.get("/update", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    const title = escapeQuote(String(request.query.title ?? ""));
    const description = escapeQuote(String(request.query.description ?? ""));
    const notes = escapeQuote(String(request.query.notes ?? "test-notes"));
    const jobInfo: JobInfo = { title, description, notes };
    await client.invokeRPC(jobPostFlow.update, workflowId, jobInfo);
    response.send("updated");
  });

  router.get("/delete", async (request, response) => {
    const workflowId = String(request.query.workflowId ?? "");
    await client.stopFlow(workflowId);
    response.send("marked as soft deleted, will be delete later after retention");
  });

  router.get("/search", async (request, response) => {
    const query = escapeQuote(String(request.query.query ?? ""));
    response.json({
      message:
        "Java Client 0.0.3 does not expose SearchFlows; "
        + "Title and JobDescription are FULL_TEXT AttributeIndexes "
        + "for when SearchFlows is available.",
      query,
    });
  });

  return router;
}
