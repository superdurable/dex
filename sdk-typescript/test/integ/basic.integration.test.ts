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
  DexError,
  ErrorSubStatus,
  FlowErrorType,
  FlowUncompletedError,
  IdReusePolicy,
  StepExecutionId,
  doubleCodec,
  stringCodec,
  voidCodec,
  type Flow,
} from "../../src/index.js";
import { AbnormalExitFlow } from "./abnormal_exit_flow.js";
import { BasicFlow } from "./basic_flow.js";
import { EmptyInputFlow } from "./empty_input_flow.js";
import { expectError, flowId, withEnvironment } from "./environment.js";
import { ImmutableStepOptionsFlow } from "./immutable_step_options_flow.js";
import { MixedWaitFlow } from "./mixed_wait_flow.js";
import { ModelInputFlow } from "./model_input_flow.js";
import { ProceedOnWaitFailureFlow } from "./proceed_on_wait_failure_flow.js";
import { SignalFlow } from "./signal_flow.js";

test("basic workflow completes and disallows duplicate IDs", async () => {
  const flow = new BasicFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("basic");
    const options = { idReusePolicy: IdReusePolicy.DISALLOW };
    await client.startFlow(flow, id, 0, options);
    assert.equal(await client.waitForFlow(id, doubleCodec, 30_000), 2);
    const duplicate = await expectError(client.startFlow(flow, id, 0, options), DexError);
    assert.equal(duplicate.subStatus, ErrorSubStatus.FLOW_ALREADY_STARTED);
  });
});

test("failed workflow ID can be reused", async () => {
  const failed = new AbnormalExitFlow();
  const succeeding = new BasicFlow();
  await withEnvironment([failed, succeeding], async ({ client }) => {
    const id = flowId("abnormal-exit-reuse");
    const options = { idReusePolicy: IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED };
    const failedRun = await client.startFlow(failed, id, 0, options);
    const failure = await expectError(
      client.waitForFlow(id, doubleCodec, 30_000),
      FlowUncompletedError,
    );
    assert.equal(failure.runId, failedRun);
    assert.equal(failure.status, "failed");
    await client.startFlow(succeeding, id, 0, options);
    assert.equal(await client.waitForFlow(id, doubleCodec, 30_000), 2);
  });
});

test("empty input and output are preserved", async () => {
  const flow = new EmptyInputFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("empty-input");
    await client.startFlow(flow, id, undefined);
    assert.equal(await client.waitForFlow(id, voidCodec, 30_000), undefined);
    const missing = await expectError(
      client.waitForFlow(flowId("missing"), voidCodec, 1_000),
      DexError,
    );
    assert.equal(missing.subStatus, ErrorSubStatus.FLOW_NOT_EXISTS);
  });
});

test("explicit Flow type and registered instance are enforced", async () => {
  const flow = new EmptyInputFlow();
  await withEnvironment([flow], async ({ client }) => {
    assert.equal(flow.getFlowType(), "test-customized-flow-type");
    const id = flowId("type-specified");
    await client.startFlow(flow, id, undefined);
    assert.equal(await client.waitForFlow(id, voidCodec, 30_000), undefined);
    await assert.rejects(
      client.startFlow(new EmptyInputFlow(), flowId("unregistered"), undefined),
      /Flow instance is not registered/,
    );
  });
});

test("model input is serialized and invalid runtime input is rejected", async () => {
  const flow = new ModelInputFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("model-input");
    await client.startFlow(flow, id, { value: 10 });
    assert.equal(await client.waitForFlow(id, doubleCodec, 30_000), 10);
    await assert.rejects(
      client.startFlow(flow as Flow<any>, flowId("wrong-input"), "wrong"),
      /invalid ModelInput/,
    );
  });
});

test("Flow config override survives Continue-As-New", async () => {
  const flow = new BasicFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("config-override");
    await client.startFlow(flow, id, 0, {
      configOverride: { continueAsNewThreshold: 1 },
    });
    assert.equal(await client.waitForFlow(id, doubleCodec, 30_000), 2);
  });
});

test("describing a missing Flow returns structured error", async () => {
  const flow = new BasicFlow();
  await withEnvironment([flow], async ({ client }) => {
    const missing = await expectError(client.describeFlow(flowId("missing")), DexError);
    assert.equal(missing.subStatus, ErrorSubStatus.FLOW_NOT_EXISTS);
  });
});

test("describe reports a running Flow", async () => {
  const flow = new SignalFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("running");
    await client.startFlow(flow, id, 0);
    assert.equal((await client.describeFlow(id)).status, "running");
    await client.stopFlow(id);
  });
});

test("waitForStepCompletion observes the requested Step", async () => {
  const flow = new BasicFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("wait-step");
    await client.startFlow(flow, id, 5);
    await client.waitForStepCompletion(id, StepExecutionId.of("BasicSecondStep"), 30_000);
    assert.equal(await client.waitForFlow(id, doubleCodec, 30_000), 7);
  });
});

test("waitFor failure can proceed to execute", async () => {
  const flow = new ProceedOnWaitFailureFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("proceed-on-wait-failure");
    await client.startFlow(flow, id, "input");
    assert.equal(await client.waitForFlow(id, stringCodec, 30_000), "input-recovered");
  });
});

test("Steps with and without waitFor compose", async () => {
  const flow = new MixedWaitFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("mixed-wait");
    await client.startFlow(flow, id, 0);
    assert.equal(await client.waitForFlow(id, doubleCodec, 30_000), 2);
  });
});

test("movement options do not mutate Step defaults", async () => {
  const flow = new ImmutableStepOptionsFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("immutable-options");
    await client.startFlow(flow, id, 0);
    const failure = await expectError(
      client.waitForFlow(id, doubleCodec, 30_000),
      FlowUncompletedError,
    );
    assert.equal(failure.status, "failed");
    assert.equal(failure.errorType, FlowErrorType.WORKER_API_FAILED);
    assert.equal(failure.message, "expected wait failure 2");
  });
});
