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

import {
  CUSTOMER_DIRECTORY_FLOW_ID,
  customerDirectoryFlow,
  emailPartition,
  type CustomerProfile,
} from "./customer-directory-flow.js";

export function createHashPartitionedAttributeMapRouter(client: Client): Router {
  const router = Router();

  router.post("/start", async (_request, response) => {
    const runId = await client.startFlow(
      customerDirectoryFlow,
      CUSTOMER_DIRECTORY_FLOW_ID,
      undefined,
      { ignoreAlreadyStarted: true },
    );
    response.json({ flowID: CUSTOMER_DIRECTORY_FLOW_ID, runID: runId });
  });

  router.put("/customer-profile", async (request, response) => {
    const profile = customerProfileFromBody(request.body);
    const { partitionName } = emailPartition(profile.emailAddress);
    const profiles = customerDirectoryFlow.customerProfilesByEmailPartition;
    response.json(
      await client.invokeRPCWithOptions(
        customerDirectoryFlow.upsertCustomerProfile,
        CUSTOMER_DIRECTORY_FLOW_ID,
        profile,
        {
          lockAttributeMapInstances: [profiles.lock(partitionName)],
          loadAttributeMapInstances: [profiles.load(partitionName)],
        },
      ),
    );
  });

  router.get("/customer-profile", async (request, response) => {
    const emailAddress = requireNonemptyString(request.query.emailAddress, "emailAddress");
    const { partitionName } = emailPartition(emailAddress);
    const profiles = customerDirectoryFlow.customerProfilesByEmailPartition;
    response.json(
      await client.invokeRPCWithOptions(
        customerDirectoryFlow.getCustomerProfileByEmail,
        CUSTOMER_DIRECTORY_FLOW_ID,
        emailAddress,
        { loadAttributeMapInstances: [profiles.load(partitionName)] },
      ),
    );
  });

  return router;
}

function customerProfileFromBody(body: unknown): CustomerProfile {
  const values = body as Partial<CustomerProfile>;
  return {
    emailAddress: requireNonemptyString(values.emailAddress, "emailAddress"),
    fullName: requireNonemptyString(values.fullName, "fullName"),
    companyName: requireNonemptyString(values.companyName, "companyName"),
    customerTier: requireNonemptyString(values.customerTier, "customerTier"),
  };
}

function requireNonemptyString(value: unknown, name: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`${name} is required`);
  }
  return value;
}
