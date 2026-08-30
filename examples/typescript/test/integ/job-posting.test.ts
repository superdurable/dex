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

import { InitialAttribute, StepExecutionId } from "@superdurable/dex";

import type { JobInfo } from "../../src/products/job-post/job-info.js";

import { acquireIntegEnvironment, releaseIntegEnvironment } from "./environment.js";

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("jobPostingUpdateReachesBothJobBoards", async () => {
  const environment = await acquireIntegEnvironment();
  const flow = environment.jobPostingFlow;
  const flowId = environment.newFlowId("job-posting");
  await environment.client.startFlow(flow, flowId, undefined, {
    ...environment.startOptions(),
    attributes: [
      InitialAttribute.of(flow.title, "Software Engineer"),
      InitialAttribute.of(flow.jobDescription, "Build reliable systems"),
      InitialAttribute.of(flow.lastUpdateTimeMillis, BigInt(Date.now())),
    ],
  });

  const updated: JobInfo = {
    title: "Senior Software Engineer",
    description: "Build durable systems",
    notes: "expanded scope",
  };
  await environment.client.invokeRPC(flow.update, flowId, updated);
  const newest: JobInfo = {
    title: "Principal Software Engineer",
    description: "Lead durable systems",
    notes: "final scope",
  };
  await environment.client.invokeRPC(flow.update, flowId, newest);
  await environment.client.waitForStepCompletion(
    flowId,
    StepExecutionId.of("UpdateLinkedInPosting", 2),
    30_000,
  );
  await environment.client.waitForStepCompletion(
    flowId,
    StepExecutionId.of("UpdateIndeedPosting", 2),
    30_000,
  );

  assert.deepEqual(await environment.client.invokeRPC(flow.get, flowId), newest);
  await environment.client.stopFlow(flowId);
});
