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

import { StepExecutionId, stringCodec } from "../../src/index.js";
import { flowId, isServerZD, withEnvironment } from "./environment.js";
import {
  StepCancellationFlow,
  cancellationScenarios,
} from "./step_cancellation_flow.js";

for (const scenario of cancellationScenarios) {
  test(`Step cancellation ${scenario}`, async () => {
    const isServerZDEngine = await isServerZD();
    const flow = new StepCancellationFlow(scenario, isServerZDEngine);
    await withEnvironment([flow], async ({ client }) => {
      const id = flowId(`typescript-cancellation-${scenario}`);
      await client.startFlow(flow, id, scenario);

      if (scenario !== "global-selector" && scenario !== "sibling-selector") {
        await withTimeout(flow.blockingStarted, 10_000);
        const selected = scenario === "heartbeat-wait-for"
          ? flow.blockingWaitFor
          : flow.blockingExecute;
        await client.waitForStepCompletion(id, StepExecutionId.of(selected.getStepType()), {
          requestId: `${id}-wait-selected-step`,
          requestTimeoutMs: 30_000,
        });
      }

      assert.equal(
        await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)),
        scenario,
      );

      if (scenario === "global-selector") {
        assert.equal(flow.firstSelectorExecuted, false);
        assert.equal(flow.secondSelectorExecuted, false);
        return;
      }
      if (scenario === "sibling-selector") {
        assert.equal(flow.firstSelectorExecuted, false);
        assert.equal(flow.secondSelectorExecuted, true);
        return;
      }
      if (scenario === "no-heartbeat") {
        if (isServerZDEngine) {
          // server-zd cancels pull handlers without a recorded heartbeat.
          await withTimeout(flow.cancellationObserved, 8_000);
          assert.equal(flow.handlerCanceled, true);
          assert.equal(flow.contextReportedCancellation, true);
        } else {
          // The Go Server leaves a non-heartbeating handler running.
          assert.equal(flow.handlerCanceled, false);
          let returned = false;
          void flow.lateHandlerReturned.then(() => { returned = true; });
          await Promise.resolve();
          assert.equal(returned, false);
        }
        await withTimeout(flow.lateHandlerReturned, 8_000);
      } else {
        await withTimeout(flow.cancellationObserved, 8_000);
        assert.equal(flow.handlerCanceled, true);
        assert.equal(flow.contextReportedCancellation, true);
      }
      let expectedInvocations = 1;
      if (scenario === "local-execute" || scenario === "local-timeout-fallback") {
        if (isServerZDEngine) {
          // server-zd has no ASYNC local invocation.
          expectedInvocations = 1;
        } else {
          // The Go Server runs local and remote ASYNC invocations.
          expectedInvocations = 2;
        }
      }
      assert.equal(flow.blockingInvocations, expectedInvocations);
      assert.equal(flow.recoveryRan, false);
    });
  });
}

async function withTimeout<T>(promise: Promise<T>, milliseconds: number): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error(`timed out after ${milliseconds}ms`)),
      milliseconds,
    );
    void promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (failure: unknown) => {
        clearTimeout(timer);
        reject(failure);
      },
    );
  });
}
