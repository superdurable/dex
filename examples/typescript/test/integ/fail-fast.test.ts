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

import { stringCodec } from "@superdurable/dex";

import { payoutOutcome } from "../../src/patterns/fail-fast/expense-reimbursement-flow.js";
import {
  acquireIntegEnvironment,
  releaseIntegEnvironment,
} from "./environment.js";

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("failFastRoutesRejectedBankAccountAfterOneAttempt", async () => {
  const environment = await acquireIntegEnvironment();
  assert.equal(
    environment.expenseReimbursementFlow.submitPayout.getStepOptions().executeRetry?.totalDurationMs,
    60 * 60_000,
  );
  const flowId = environment.newFlowId("pattern-fail-fast");
  await environment.client.startFlow(
    environment.expenseReimbursementFlow,
    flowId,
    { amountCents: 12_000, bankAccount: "not-a-bank-account" },
    environment.startOptions(),
  );

  const result = await environment.client.waitForFlow(flowId, 45_000);
  assert.equal(result.status, "completed");
  assert.equal(result.singleOutput(stringCodec), "rejected: bank account is invalid");
  const [outcome] = await environment.client.getAttributes(flowId, payoutOutcome);
  assert.deepEqual(outcome, {
    status: "REJECTED",
    detail: "bank account is invalid",
    submitAttempt: 1,
  });
  assert.equal(environment.paymentProvider.countSubmissions(flowId), 1);
});

test("failFastRetriesProviderTimeout", async () => {
  const environment = await acquireIntegEnvironment();
  const flowId = environment.newFlowId("pattern-fail-fast");
  await environment.client.startFlow(
    environment.expenseReimbursementFlow,
    flowId,
    { amountCents: 12_000, bankAccount: "12345678" },
    environment.startOptions(),
  );

  const result = await environment.client.waitForFlow(flowId, 45_000);
  assert.equal(result.status, "completed");
  assert.equal(result.singleOutput(stringCodec), `payout-${flowId}`);
  const [outcome] = await environment.client.getAttributes(flowId, payoutOutcome);
  assert.deepEqual(outcome, { status: "PAID", detail: `payout-${flowId}`, submitAttempt: 2 });
  assert.equal(environment.paymentProvider.countSubmissions(flowId), 2);
});
