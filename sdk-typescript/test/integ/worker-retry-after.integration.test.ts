// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import test from "node:test";

import { flowId, withEnvironment } from "./environment.js";
import {
  assertWorkerFailureStackTrace,
  awaitLiveWorkerFailure,
} from "./flow-service-client.js";
import {
  EXECUTE_RETRY_AFTER_DETAIL,
  RETRY_AFTER_SECONDS,
  RETRY_POLICY_INTERVAL_SECONDS,
  WAIT_FOR_RETRY_AFTER_DETAIL,
  WorkerRetryAfterExecuteFlow,
  WorkerRetryAfterWaitForFlow,
} from "./worker-retry-after-flow.js";

test("waitFor retry-after exposes stack trace and custom delay", async () => {
  const flow = new WorkerRetryAfterWaitForFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("wait-retry-after");
    const startedAt = Date.now();
    const runId = await client.startFlow(flow, id, undefined);
    const failure = await awaitLiveWorkerFailure(id, runId);
    assertWorkerFailureStackTrace(failure, WAIT_FOR_RETRY_AFTER_DETAIL);
    const result = await client.waitForFlow(id, 30_000);
    assert.equal(result.status, "completed");
    const elapsed = Date.now() - startedAt;
    assert.ok(elapsed >= RETRY_AFTER_SECONDS * 1_000);
    assert.ok(elapsed < RETRY_POLICY_INTERVAL_SECONDS * 1_000);
  });
});

test("execute retry-after exposes stack trace and custom delay", async () => {
  const flow = new WorkerRetryAfterExecuteFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("execute-retry-after");
    const startedAt = Date.now();
    const runId = await client.startFlow(flow, id, undefined);
    const failure = await awaitLiveWorkerFailure(id, runId);
    assertWorkerFailureStackTrace(failure, EXECUTE_RETRY_AFTER_DETAIL);
    const result = await client.waitForFlow(id, 30_000);
    assert.equal(result.status, "completed");
    const elapsed = Date.now() - startedAt;
    assert.ok(elapsed >= RETRY_AFTER_SECONDS * 1_000);
    assert.ok(elapsed < RETRY_POLICY_INTERVAL_SECONDS * 1_000);
  });
});
