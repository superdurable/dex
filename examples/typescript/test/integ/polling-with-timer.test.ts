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

import { StepExecutionId, TimerId, stringCodec } from "@superdurable/dex";

import { JobNotFoundError } from "../../src/patterns/polling/fake-job-service.js";
import { POLL_INTERVAL_TIMER_ID } from "../../src/patterns/polling-with-timer/polling-with-timer-flow.js";
import {
  acquireIntegEnvironment,
  awaitCondition,
  releaseIntegEnvironment,
  type IntegEnvironment,
} from "./environment.js";

const WAIT_TIMEOUT_MS = 45_000;
// Mirrors the example's round limit.
const MAX_POLL_ROUNDS = 60;

let environment: IntegEnvironment;

test.before(async () => {
  environment = await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("polling with timer moves to the next round while the job runs", async () => {
  const flowId = environment.newFlowId("pattern-polling-with-timer");
  environment.pollingJobs.keepJobRunning(flowId);
  await startFlow(flowId);

  await skipPollIntervalTimer(flowId, 1);
  await awaitCondition(
    async () => environment.pollingJobs.countStatusChecks(flowId),
    (statusChecks) => statusChecks === 1,
    WAIT_TIMEOUT_MS,
    `round 1 of ${flowId} never checked the running job`,
  );
  environment.pollingJobs.completeJob(flowId);
  await skipPollIntervalTimer(flowId, 2);

  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", `${flowId}: ${result.errorMessage ?? ""}`);
  assert.equal(result.singleOutput(stringCodec), `artifact for ${flowId}`);
  assert.equal(environment.pollingJobs.countStatusChecks(flowId), 2);
});

test("polling with timer fails after the maximum rounds", async () => {
  const flowId = environment.newFlowId("pattern-polling-with-timer-rounds");
  environment.pollingJobs.keepJobRunning(flowId);
  await startFlow(flowId);

  for (let round = 1; round <= MAX_POLL_ROUNDS; round++) {
    await skipPollIntervalTimer(flowId, round);
  }

  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "failed", `${flowId}: ${result.errorMessage ?? ""}`);
  assert.match(result.errorMessage ?? "", new RegExp(`did not finish in ${MAX_POLL_ROUNDS} rounds`));
  assert.equal(environment.pollingJobs.countStatusChecks(flowId), MAX_POLL_ROUNDS);
});

test("polling with timer retries a transient status failure", async () => {
  const flowId = environment.newFlowId("pattern-polling-with-timer-retry");
  environment.pollingJobs.failNextStatusChecks(flowId, 1);
  await startFlow(flowId);
  await completeJobWhenStarted(flowId);

  await skipPollIntervalTimer(flowId, 1);
  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", `${flowId}: ${result.errorMessage ?? ""}`);
  assert.equal(environment.pollingJobs.countStatusChecks(flowId), 2);
});

test("polling with timer records the failure when retries run out", async () => {
  const flowId = environment.newFlowId("pattern-polling-with-timer-recovery");
  environment.pollingJobs.failNextStatusChecks(flowId, 3);
  await startFlow(flowId);
  await completeJobWhenStarted(flowId);

  await skipPollIntervalTimer(flowId, 1);
  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "failed", `${flowId}: ${result.errorMessage ?? ""}`);
  assert.ok((result.errorMessage ?? "").includes(`checking job ${flowId} failed`), result.errorMessage);
  assert.equal(environment.pollingJobs.countStatusChecks(flowId), 3);
});

async function startFlow(flowId: string): Promise<void> {
  await environment.client.startFlow(
    environment.pollingWithTimerFlow,
    flowId,
    undefined,
    environment.startOptions(),
  );
}

async function completeJobWhenStarted(flowId: string): Promise<void> {
  await awaitCondition(
    async () => {
      try {
        environment.pollingJobs.completeJob(flowId);
        return true;
      } catch (error) {
        if (error instanceof JobNotFoundError) {
          return false;
        }
        throw error;
      }
    },
    (isCompleted) => isCompleted,
    WAIT_TIMEOUT_MS,
    `StartJob never started the job of ${flowId}`,
  );
}

async function skipPollIntervalTimer(flowId: string, round: number): Promise<void> {
  await awaitCondition(
    async () => {
      try {
        await environment.client.skipTimer(
          flowId,
          StepExecutionId.of("CheckJobStatus", round),
          TimerId.byConditionId(POLL_INTERVAL_TIMER_ID),
        );
        return true;
      } catch {
        return false;
      }
    },
    (isSkipped) => isSkipped,
    WAIT_TIMEOUT_MS,
    `the interval Timer of round ${round} of ${flowId} was never skipped`,
  );
}
