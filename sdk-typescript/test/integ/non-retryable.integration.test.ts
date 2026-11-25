// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { flowId, withEnvironment } from "./environment.js";
import {
  NON_RETRYABLE_DETAIL,
  NonRetryableExecuteFlow,
  NonRetryableWaitForFlow,
  attemptsByFlowId,
  recoveryByFlowId,
} from "./non-retryable-flow.js";

// Far below the hourly retry interval: completing in time proves no retry ran.
const WAIT_TIMEOUT_MS = 30_000;

function assertRecoveredAfterOneAttempt(id: string): void {
  assert.equal(attemptsByFlowId.get(id), 1);
  const recovery = recoveryByFlowId.get(id);
  assert.equal(recovery?.detail, NON_RETRYABLE_DETAIL);
  assert.equal(recovery?.errorType, "CardRejectedError");
}

test("non-retryable execute failure skips retries and proceeds to the recovery Step", async () => {
  const flow = new NonRetryableExecuteFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("non-retryable-execute");
    await client.startFlow(flow, id, "order");
    const result = await client.waitForFlow(id, WAIT_TIMEOUT_MS);
    assert.equal(result.status, "completed");
    assertRecoveredAfterOneAttempt(id);
  });
});

test("non-retryable waitFor failure skips retries and proceeds to execute", async () => {
  const flow = new NonRetryableWaitForFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("non-retryable-wait-for");
    await client.startFlow(flow, id, undefined);
    const result = await client.waitForFlow(id, WAIT_TIMEOUT_MS);
    assert.equal(result.status, "completed");
    assertRecoveredAfterOneAttempt(id);
  });
});
