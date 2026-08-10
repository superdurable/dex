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
  FlowNotActiveError,
  type Client,
} from "@superdurable/dex";

import { startOptions } from "../config/env.js";
import { employerOptInFlow } from "../workflow/shortlistcandidates/employer-opt-in-flow.js";
import type { EmployerOptInInput } from "../workflow/shortlistcandidates/employer-opt-in-input.js";
import { shortlistFlow } from "../workflow/shortlistcandidates/shortlist-flow.js";
import type { ShortlistInput } from "../workflow/shortlistcandidates/shortlist-input.js";
import { employerOptIn, isOptedIn, shortlist } from "../workflow/shortlistcandidates/workflow-ids.js";

export function createShortlistCandidatesRouter(client: Client): Router {
  const router = Router();

  router.post("/opt_in", async (request, response) => {
    const employerId = String(request.body?.employerId ?? "");
    const workflowId = employerOptIn(employerId);
    const input: EmployerOptInInput = { employerId };
    try {
      await client.startFlow(employerOptInFlow, workflowId, input, startOptions());
    } catch (failure) {
      if (failure instanceof FlowAlreadyStartedError) {
        response.send(`Employer ${employerId} has already opted in`);
        return;
      }
      throw failure;
    }
    response.send(`Started workflowId: ${workflowId}`);
  });

  router.post("/opt_out", async (request, response) => {
    const employerId = String(request.body?.employerId ?? "");
    const workflowId = employerOptIn(employerId);
    try {
      await client.invokeRPC(employerOptInFlow.optOut, workflowId);
    } catch (failure) {
      if (failure instanceof FlowNotActiveError) {
        response.send(`Employer ${employerId} is not in the opt-in status`);
        return;
      }
      throw failure;
    }
    response.send(`Employer ${employerId} has opted out`);
  });

  router.get("/is_opted_in", async (request, response) => {
    const employerId = String(request.query.employerId ?? "test-employer");
    response.json(await isOptedIn(client, employerOptInFlow, employerId));
  });

  router.post("/shortlist", async (request, response) => {
    const employerId = String(request.body?.employerId ?? "");
    const candidateId = String(request.body?.candidateId ?? "");

    if (!(await isOptedIn(client, employerOptInFlow, employerId))) {
      response.send(`Do nothing for ${employerId} because of no opt-in`);
      return;
    }

    const workflowId = shortlist(employerId, candidateId);
    const input: ShortlistInput = { employerId, candidateId };
    try {
      await client.startFlow(shortlistFlow, workflowId, input, startOptions());
    } catch (failure) {
      if (failure instanceof FlowAlreadyStartedError) {
        response.send(`Already running workflowId: ${workflowId}`);
        return;
      }
      throw failure;
    }
    response.send(`Started workflowId: ${workflowId}`);
  });

  router.post("/revoke_shortlist", async (request, response) => {
    const employerId = String(request.body?.employerId ?? "");
    const candidateId = String(request.body?.candidateId ?? "");
    const workflowId = shortlist(employerId, candidateId);
    try {
      await client.publish(workflowId, shortlistFlow.revokeShortlist, undefined);
    } catch (failure) {
      if (failure instanceof FlowNotActiveError) {
        response.send(`No running workflow to revoke for ${employerId}-${candidateId}`);
        return;
      }
      throw failure;
    }
    response.send(`Revoked shortlist for ${employerId}-${candidateId}`);
  });

  router.get("/email_sent_timestamp", async (request, response) => {
    const employerId = String(request.query.employerId ?? "test-employer");
    const candidateId = String(request.query.candidateId ?? "test-candidate");
    const workflowId = shortlist(employerId, candidateId);
    try {
      const timestamp = await client.invokeRPC(
        shortlistFlow.getEmailSentTimestamp,
        workflowId,
      );
      response.json(Number(timestamp));
    } catch (failure) {
      if (failure instanceof FlowNotActiveError) {
        response.sendStatus(404);
        return;
      }
      throw failure;
    }
  });

  return router;
}
