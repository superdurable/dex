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

import {
  FlowErrorType,
  FlowUncompletedError,
  LongPollTimeoutError,
  StopType,
  doubleCodec,
  type Client,
  type FlowErrorType as FlowErrorTypeValue,
  type FlowStatus,
  type StopType as StopTypeValue,
} from "../../src/index.js";
import { EmptyDecisionFlow } from "./empty_decision_flow.js";
import { expectError, flowId, withEnvironment } from "./environment.js";
import { ForceFailFlow } from "./force_fail_flow.js";
import { SignalFlow } from "./signal_flow.js";
import { StateFailureFlow } from "./state_failure_flow.js";
import { StateTimeoutFlow } from "./state_timeout_flow.js";

test("waitForFlow reports long-poll timeout while Flow remains running", async () => {
  const flow = new SignalFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("wait-timeout");
    await client.startFlow(flow, id, 1);
    const failure = await expectError(
      client.waitForFlow(id, 1_000).then((result) => result.singleOutput(doubleCodec)),
      LongPollTimeoutError,
    );
    assert.equal(failure.flowId, id);
    await client.stopFlow(id);
  });
});

test("Flow execution timeout is reported without results", async () => {
  const flow = new SignalFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("flow-timeout");
    const runId = await client.startFlow(flow, id, 1, { timeoutMs: 1_000 });
    const failure = await waitForFailure(client, id);
    assertFailure(failure, runId, "timedOut", undefined, undefined, 0);
  });
});

for (const name of ["cancelled", "cancelled-without-run-id"] as const) {
  test(`Flow ${name} is reported without an error type`, async () => {
    await assertStoppedFlow(StopType.CANCEL, undefined, "cancelled", undefined, undefined);
  });
}

test("terminated Flow preserves its terminal status", async () => {
  await assertStoppedFlow(StopType.TERMINATE, "terminated", "terminated", undefined, undefined);
});

test("Flow failed through Client API preserves reason", async () => {
  await assertStoppedFlow(
    StopType.FAIL,
    "fail by API",
    "failed",
    FlowErrorType.CLIENT_API_FAILED,
    "fail by API",
  );
});

test("forceFail produces a Step-decision failure", async () => {
  const flow = new ForceFailFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("force-fail");
    const runId = await client.startFlow(flow, id, 5);
    const failure = await waitForFailure(client, id);
    assertFailure(
      failure,
      runId,
      "failed",
      FlowErrorType.STEP_DECISION_FAILED,
      "a failing message",
      0,
    );
  });
});

test("Worker API failure preserves the callback message", async () => {
  const flow = new StateFailureFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("worker-api-failure");
    const runId = await client.startFlow(flow, id, 5);
    const failure = await waitForFailure(client, id);
    assert.equal(failure.runId, runId);
    assert.equal(failure.status, "failed");
    assert.equal(failure.errorType, FlowErrorType.WORKER_API_FAILED);
    assert.match(failure.message, /test api failing/);
    assert.equal(failure.completions.length, 0);
  });
});

test("Worker method timeout fails the Flow", async () => {
  const flow = new StateTimeoutFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("worker-api-timeout");
    const runId = await client.startFlow(flow, id, 5);
    const failure = await waitForFailure(client, id);
    assert.equal(failure.runId, runId);
    assert.equal(failure.status, "failed");
    assert.equal(failure.errorType, FlowErrorType.WORKER_API_FAILED);
    assert.match(failure.message, /timeout/i);
    assert.equal(failure.completions.length, 0);
  });
});

test("empty goToMulti decision fails the Flow", async () => {
  const flow = new EmptyDecisionFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("empty-decision");
    const runId = await client.startFlow(flow, id, 5);
    const failure = await waitForFailure(client, id);
    assert.equal(failure.runId, runId);
    assert.equal(failure.status, "failed");
    assert.equal(failure.errorType, FlowErrorType.WORKER_API_FAILED);
    assert.match(failure.message, /goToMulti requires a movement/);
    assert.equal(failure.completions.length, 0);
  });
});

async function assertStoppedFlow(
  stopType: StopTypeValue,
  reason: string | undefined,
  expectedStatus: FlowStatus,
  expectedErrorType: FlowErrorTypeValue | undefined,
  expectedMessage: string | undefined,
): Promise<void> {
  const flow = new SignalFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("stopped");
    const runId = await client.startFlow(flow, id, 1);
    await client.stopFlow(id, {
      type: stopType,
      ...(reason === undefined ? {} : { reason }),
    });
    const failure = await waitForFailure(client, id);
    assertFailure(failure, runId, expectedStatus, expectedErrorType, expectedMessage, 0);
  });
}

async function waitForFailure(client: Client, id: string): Promise<FlowUncompletedError> {
  return expectError(client.waitForFlow(id, 15_000).then((result) => result.singleOutput(doubleCodec)), FlowUncompletedError);
}

function assertFailure(
  failure: FlowUncompletedError,
  runId: string,
  status: FlowStatus,
  errorType: FlowErrorTypeValue | undefined,
  message: string | undefined,
  resultCount: number,
): void {
  assert.equal(failure.runId, runId);
  assert.equal(failure.status, status);
  assert.equal(failure.errorType, errorType);
  assert.equal(failure.message, message ?? "");
  assert.equal(failure.completions.length, resultCount);
}
