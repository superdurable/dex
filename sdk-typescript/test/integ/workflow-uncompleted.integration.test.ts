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
  type FlowResult,
  FlowTimeoutPolicy,
  LongPollTimeoutError,
  StopType,
  doubleCodec,
  forceComplete,
  stringCodec,
  type Client,
  type Context,
  type FlowErrorType as FlowErrorTypeValue,
  type FlowStatus,
  type StepDecision,
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
    await client.startFlow(flow, id, 1, { timeoutMs: 1_000 });
    const failure = await waitForFailure(client, id);
    assertFailure(
      failure,
      "failed",
      FlowErrorType.FLOW_TIMEOUT,
      "Flow timed out after 1 seconds",
      0,
    );
  });
});

class TimeoutHandlerFlow extends SignalFlow {
  public handleTimeout(_context: Context): StepDecision {
    return forceComplete("expired");
  }
}

test("Flow timeout handler completes the Flow", async () => {
  const flow = new TimeoutHandlerFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("flow-timeout-handler");
    await client.startFlow(flow, id, 1, { timeoutMs: 1_000 });
    assert.equal(
      await client.waitForFlow(id, 15_000).then((result) => result.singleOutput(stringCodec)),
      "expired",
    );
  });
});

test("cancel policy overrides a registered Flow timeout handler", async () => {
  const flow = new TimeoutHandlerFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("flow-timeout-handler-cancel");
    await client.startFlow(flow, id, 1, {
      timeoutMs: 1_000,
      timeoutPolicy: FlowTimeoutPolicy.CANCEL,
    });
    const failure = await waitForFailure(client, id);
    assertFailure(failure, "cancelled", undefined, undefined, 0);
  });
});

test("handler policy requires a registered Flow timeout handler", async () => {
  const flow = new SignalFlow();
  await withEnvironment([flow], async ({ client }) => {
    await assert.rejects(
      client.startFlow(flow, flowId("flow-timeout-policy-without-timeout"), 1, {
        timeoutPolicy: FlowTimeoutPolicy.CANCEL,
      }),
      /requires a positive timeout/,
    );

    await assert.rejects(
      client.startFlow(flow, flowId("flow-timeout-handler-missing"), 1, {
        timeoutMs: 1_000,
        timeoutPolicy: FlowTimeoutPolicy.HANDLER,
      }),
      /does not implement handleTimeout/,
    );
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
    await client.startFlow(flow, id, 5);
    const failure = await waitForFailure(client, id);
    assertFailure(
      failure,
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
    await client.startFlow(flow, id, 5);
    const failure = await waitForFailure(client, id);
    assert.equal(failure.status, "failed");
    assert.equal(failure.errorType, FlowErrorType.WORKER_API_FAILED);
    assert.match(failure.errorMessage ?? "", /test api failing/);
    assert.equal(failure.completions.length, 0);
  });
});

test("Worker method timeout fails the Flow", async () => {
  const flow = new StateTimeoutFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("worker-api-timeout");
    await client.startFlow(flow, id, 5);
    const failure = await waitForFailure(client, id);
    assert.equal(failure.status, "failed");
    assert.equal(failure.errorType, FlowErrorType.WORKER_API_FAILED);
    assert.match(failure.errorMessage ?? "", /timeout/i);
    assert.equal(failure.completions.length, 0);
  });
});

test("empty goToMulti decision fails the Flow", async () => {
  const flow = new EmptyDecisionFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("empty-decision");
    await client.startFlow(flow, id, 5);
    const failure = await waitForFailure(client, id);
    assert.equal(failure.status, "failed");
    assert.equal(failure.errorType, FlowErrorType.WORKER_API_FAILED);
    assert.match(failure.errorMessage ?? "", /goToMulti requires a movement/);
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
    await client.startFlow(flow, id, 1);
    await client.stopFlow(id, {
      type: stopType,
      ...(reason === undefined ? {} : { reason }),
    });
    const failure = await waitForFailure(client, id);
    assertFailure(failure, expectedStatus, expectedErrorType, expectedMessage, 0);
  });
}

async function waitForFailure(client: Client, id: string): Promise<FlowResult> {
  return client.waitForFlow(id, 15_000);
}

function assertFailure(
  failure: FlowResult,
  status: FlowStatus,
  errorType: FlowErrorTypeValue | undefined,
  message: string | undefined,
  resultCount: number,
): void {
  assert.equal(failure.status, status);
  assert.equal(failure.errorType, errorType);
  assert.equal(failure.errorMessage, message);
  assert.equal(failure.completions.length, resultCount);
}
