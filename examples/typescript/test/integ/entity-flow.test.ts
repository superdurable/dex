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

import assert from "node:assert/strict";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";

import {
  FlowAlreadyStartedError,
  IdReusePolicy,
  InitialAttribute,
  RpcLockConflictError,
} from "@superdurable/dex";

import {
  accountArchive,
  customerAccountFlowId,
} from "../../src/patterns/entity-flow/customer-account-flow.js";
import {
  acquireIntegEnvironment,
  releaseIntegEnvironment,
  type IntegEnvironment,
} from "./environment.js";

const CONCURRENT_DEPOSITS = 8;
const CLOSE_TIMEOUT_MS = 45_000;

let environment: IntegEnvironment;

test.before(async () => {
  environment = await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test(
  "entity Flow keeps one customer account until it is retired",
  { timeout: CLOSE_TIMEOUT_MS + 15_000 },
  async () => {
    const { client, customerAccountFlow: flow } = environment;
    const flowId = customerAccountFlowId(environment.newFlowId("entity-flow"));
    const options = {
      idReusePolicy: IdReusePolicy.DISALLOW,
      attributes: [
        InitialAttribute.of(flow.displayName, "Ada"),
        InitialAttribute.of(flow.email, "ada@example.com"),
        InitialAttribute.of(flow.creditBalance, 0n),
        InitialAttribute.of(flow.retired, false),
      ],
    };
    await client.startFlow(flow, flowId, undefined, options);
    await assert.rejects(
      client.startFlow(flow, flowId, undefined, options),
      FlowAlreadyStartedError,
    );

    await client.invokeRPC(flow.updateContactDetails, flowId, {
      displayName: "Ada Lovelace",
      email: "ada@lovelace.example",
    });
    await assert.rejects(
      client.invokeRPC(flow.updateContactDetails, flowId, {
        displayName: "Ada",
        email: "not-an-email",
      }),
      /email must contain @/,
    );

    await Promise.all(
      Array.from({ length: CONCURRENT_DEPOSITS }, () => depositWithRetry(flowId, 5)),
    );
    await assert.rejects(
      client.invokeRPC(flow.adjustCreditBalance, flowId, { amount: -1000 }),
      /below zero/,
    );
    await assert.rejects(
      client.invokeRPC(flow.adjustCreditBalance, flowId, {
        amount: "5" as unknown as number,
      }),
      /safe integer/,
    );
    await assert.rejects(
      client.invokeRPC(flow.adjustCreditBalance, flowId, {
        amount: Number.MAX_SAFE_INTEGER,
      }),
      /largest safe integer/,
    );

    const [displayName, email, creditBalance] = await client.getAttributes(
      flowId,
      flow.displayName,
      flow.email,
      flow.creditBalance,
    );
    assert.equal(displayName, "Ada Lovelace");
    assert.equal(email, "ada@lovelace.example");
    assert.equal(creditBalance, BigInt(5 * CONCURRENT_DEPOSITS));

    await assert.rejects(
      client.invokeRPC(flow.retireAccount, flowId),
      /before retiring/,
    );
    assert.equal((await client.describeFlow(flowId)).status, "running");

    await client.invokeRPC(flow.adjustCreditBalance, flowId, {
      amount: -5 * CONCURRENT_DEPOSITS,
    });
    accountArchive.makeUnavailable(flowId);
    await client.invokeRPC(flow.retireAccount, flowId);
    await waitForArchiveAttempts(flowId, 1);
    assert.equal((await client.describeFlow(flowId)).status, "running");
    await assert.rejects(
      client.invokeRPC(flow.adjustCreditBalance, flowId, { amount: 10 }),
      /account is retired/,
    );
    await assert.rejects(
      client.invokeRPC(flow.updateContactDetails, flowId, {
        displayName: "Ada Byron",
        email: "ada@byron.example",
      }),
      /account is retired/,
    );
    await client.invokeRPC(flow.retireAccount, flowId);
    await waitForArchiveAttempts(flowId, 2);
    assert.equal((await client.describeFlow(flowId)).status, "running");

    accountArchive.loseNextResponse(flowId);
    accountArchive.makeAvailable(flowId);
    const result = await client.waitForFlow(flowId, CLOSE_TIMEOUT_MS);
    assert.equal(result.status, "completed");
    const attempts = accountArchive.attemptStepExecutionIdsFor(flowId);
    assert.ok(attempts.length >= 4, `archive attempts: ${attempts.length}`);
    assert.equal(new Set(attempts).size, 1);
    assert.equal(accountArchive.archiveRecordCount(flowId), 1);
    const [closedBalance, closedRetired] = await client.getAttributes(
      flowId,
      flow.creditBalance,
      flow.retired,
    );
    assert.equal(closedBalance, 0n);
    assert.equal(closedRetired, true);

    await assert.rejects(
      client.startFlow(flow, flowId, undefined, options),
      FlowAlreadyStartedError,
    );
  },
);

async function waitForArchiveAttempts(flowId: string, count: number): Promise<void> {
  const deadline = Date.now() + CLOSE_TIMEOUT_MS;
  while (accountArchive.attemptStepExecutionIdsFor(flowId).length < count) {
    if (Date.now() > deadline) {
      throw new Error(`timed out waiting for ${count} archive attempts`);
    }
    await delay(50);
  }
}

async function depositWithRetry(flowId: string, amount: number): Promise<void> {
  for (let attempt = 1; attempt <= 50; attempt += 1) {
    try {
      await environment.client.invokeRPC(
        environment.customerAccountFlow.adjustCreditBalance,
        flowId,
        { amount },
      );
      return;
    } catch (error) {
      if (!(error instanceof RpcLockConflictError)) {
        throw error;
      }
      await delay(attempt * 50);
    }
  }
  throw new Error("credit deposit kept conflicting on the balance lock");
}
