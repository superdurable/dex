// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import test from "node:test";

import {
  FlowNotFoundError,
  StopType,
  type ActiveStepExecution,
  type Client,
} from "../../src/index.js";
import { expectError, flowId, withEnvironment } from "./environment.js";
import {
  GET_FLOW_STATE_FAILURE_DETAIL,
  GetFlowStateFailingFlow,
  GetFlowStateWaitingFlow,
} from "./get_flow_state_flow.js";

test("getFlowState reports the retrying Step and its last failure", async () => {
  const flow = new GetFlowStateFailingFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("get-flow-state-failing");
    const startedAt = Date.now();
    await client.startFlow(flow, id, undefined);

    const step = await pollActiveStep(
      client,
      id,
      (candidate) => candidate.lastFailure !== undefined && candidate.invocation?.phase === "backoff",
    );
    assert.equal(step.stepType, "GetFlowStateFailingStep");
    assert.equal(step.stepExecutionId, "GetFlowStateFailingStep-1");
    assert.equal(step.executionNumber, 1);
    assert.equal(step.phase, "active");
    assert.equal(step.invocation?.method, "execute");
    assert.equal(step.invocation?.attempt, 2);
    assert.equal(step.invocation?.pendingReason, undefined);
    assert.ok(step.invocation?.backoffFiresAt instanceof Date);
    assert.ok(step.invocation.backoffFiresAt.getTime() > startedAt);
    assert.equal(step.lastFailure?.attempt, 1);
    assert.equal(step.lastFailure?.errorType, "ChargeDeclinedError");
    assert.equal(step.lastFailure?.detail, GET_FLOW_STATE_FAILURE_DETAIL);
    assert.ok(step.lastFailure?.stackTrace.includes(GET_FLOW_STATE_FAILURE_DETAIL));

    await client.stopFlow(id, { type: StopType.TERMINATE, reason: "test cleanup" });
    const closed = await client.waitForFlow(id, 30_000);
    assert.equal(closed.status, "terminated");
    const closedState = await client.getFlowState(id);
    assert.deepEqual(closedState.activeStepExecutions, []);
  });
});

test("getFlowState reports no failure for a healthy running Flow", async () => {
  const flow = new GetFlowStateWaitingFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("get-flow-state-waiting");
    await client.startFlow(flow, id, undefined);

    const step = await pollActiveStep(client, id, (candidate) => candidate.phase === "waiting");
    assert.equal(step.stepType, "GetFlowStateWaitingStep");
    assert.equal(step.executionNumber, 1);
    assert.equal(step.invocation, undefined);
    assert.equal(step.lastFailure, undefined);
    assert.equal((await client.describeFlow(id)).status, "running");

    await client.stopFlow(id, { type: StopType.TERMINATE, reason: "test cleanup" });
  });
});

test("getFlowState rejects a missing Flow", async () => {
  const flow = new GetFlowStateWaitingFlow();
  await withEnvironment([flow], async ({ client }) => {
    const missingId = flowId("get-flow-state-missing");
    const error = await expectError(client.getFlowState(missingId), FlowNotFoundError);
    assert.equal(error.flowId, missingId);
    await assert.rejects(client.getFlowState(""), TypeError);
  });
});

async function pollActiveStep(
  client: Client,
  id: string,
  isReady: (step: ActiveStepExecution) => boolean,
  timeoutMs = 15_000,
): Promise<ActiveStepExecution> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const state = await client.getFlowState(id);
    const step = state.activeStepExecutions.find(isReady);
    if (step !== undefined) {
      assert.equal(state.activeStepExecutions.length, 1);
      return step;
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`Flow ${id} did not report the expected active Step within ${timeoutMs} ms`);
}
