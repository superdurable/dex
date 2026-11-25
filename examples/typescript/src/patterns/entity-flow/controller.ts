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

import { Router, type Request } from "express";

import {
  FlowAlreadyStartedError,
  IdReusePolicy,
  InitialAttribute,
  type Client,
} from "@superdurable/dex";

import { requiredString } from "../shared/http-helpers.js";
import {
  customerAccountFlow,
  customerAccountFlowId,
  validateContactDetails,
  validateCreditAmount,
  type ContactDetails,
} from "./customer-account-flow.js";

export function createEntityFlowRouter(client: Client): Router {
  const router = Router();

  router.post("/account", async (request, response) => {
    const accountId = requiredString(request.body?.accountId, "accountId");
    const contact = contactDetailsFromRequest(request);
    const flowId = customerAccountFlowId(accountId);
    try {
      const runId = await client.startFlow(customerAccountFlow, flowId, undefined, {
        idReusePolicy: IdReusePolicy.DISALLOW,
        attributes: [
          InitialAttribute.of(customerAccountFlow.displayName, contact.displayName),
          InitialAttribute.of(customerAccountFlow.email, contact.email),
          InitialAttribute.of(customerAccountFlow.creditBalance, 0n),
          InitialAttribute.of(customerAccountFlow.retired, false),
        ],
      });
      response.json({ flowID: flowId, runID: runId });
    } catch (error) {
      if (error instanceof FlowAlreadyStartedError) {
        response.status(409).json({ error: "account already exists" });
        return;
      }
      throw error;
    }
  });

  router.post("/account/contact", async (request, response) => {
    const accountId = requiredString(request.body?.accountId, "accountId");
    await client.invokeRPC(
      customerAccountFlow.updateContactDetails,
      customerAccountFlowId(accountId),
      contactDetailsFromRequest(request),
    );
    response.send("Updated contact details");
  });

  router.post("/account/credits", async (request, response) => {
    const accountId = requiredString(request.body?.accountId, "accountId");
    let amount: number;
    try {
      amount = validateCreditAmount(request.body?.amount);
    } catch (error) {
      response.status(400).json({ error: (error as Error).message });
      return;
    }
    const balance = await client.invokeRPC(
      customerAccountFlow.adjustCreditBalance,
      customerAccountFlowId(accountId),
      { amount },
    );
    response.json({ creditBalance: Number(balance) });
  });

  router.get("/account", async (request, response) => {
    const accountId = requiredString(request.query.accountId, "accountId");
    const [displayName, email, creditBalance] = await client.getAttributes(
      customerAccountFlowId(accountId),
      customerAccountFlow.displayName,
      customerAccountFlow.email,
      customerAccountFlow.creditBalance,
    );
    response.json({
      accountId,
      displayName: displayName ?? "",
      email: email ?? "",
      creditBalance: Number(creditBalance ?? 0n),
    });
  });

  router.post("/account/retire", async (request, response) => {
    const accountId = requiredString(request.query.accountId, "accountId");
    await client.invokeRPC(
      customerAccountFlow.retireAccount,
      customerAccountFlowId(accountId),
    );
    response.send("Retired account");
  });

  return router;
}

function contactDetailsFromRequest(request: Request): ContactDetails {
  const contact: ContactDetails = {
    displayName: requiredString(request.body?.displayName, "displayName"),
    email: requiredString(request.body?.email, "email"),
  };
  validateContactDetails(contact);
  return contact;
}
