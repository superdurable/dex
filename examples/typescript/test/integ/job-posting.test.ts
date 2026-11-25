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
import { once } from "node:events";
import type { AddressInfo } from "node:net";
import test from "node:test";

import { InitialAttribute, StepExecutionId } from "@superdurable/dex";
import express from "express";

import { createJobPostingRouter } from "../../src/products/job-post/controller.js";
import type { JobInfo } from "../../src/products/job-post/job-info.js";

import { acquireIntegEnvironment, awaitCondition, releaseIntegEnvironment, type IntegEnvironment } from "./environment.js";

let environment: IntegEnvironment;

test.before(async () => {
  environment = await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("jobPostingUpdateReachesBothJobBoards", async () => {
  const flow = environment.jobPostingFlow;
  const flowId = environment.newFlowId("job-posting");
  await environment.client.startFlow(flow, flowId, undefined, {
    ...environment.startOptions(),
    attributes: [
      InitialAttribute.of(flow.title, "Software Engineer"),
      InitialAttribute.of(flow.jobDescription, "Build reliable systems"),
      InitialAttribute.of(flow.lastUpdateTimeMillis, BigInt(Date.now())),
      InitialAttribute.of(flow.updateVersion, 0),
    ],
  });
  await environment.client.waitForStepCompletion(
    flowId,
    StepExecutionId.of("InitStep", 1),
    { requestTimeoutMs: 30_000 },
  );

  const updated: JobInfo = {
    title: "Senior Software Engineer",
    description: "Build durable systems",
    notes: "expanded scope",
  };
  assert.equal(await environment.client.invokeRPC(flow.update, flowId, updated), 1);
  const newest: JobInfo = {
    title: "Principal Software Engineer",
    description: "Lead durable systems",
    notes: "final scope",
  };
  assert.equal(await environment.client.invokeRPC(flow.update, flowId, newest), 2);
  await environment.client.waitForStepCompletion(
    flowId,
    StepExecutionId.of("UpdateLinkedInPosting", 2),
    { requestTimeoutMs: 30_000 },
  );
  await environment.client.waitForStepCompletion(
    flowId,
    StepExecutionId.of("UpdateIndeedPosting", 2),
    { requestTimeoutMs: 30_000 },
  );

  assert.deepEqual(await environment.client.invokeRPC(flow.get, flowId), newest);
  await environment.client.stopFlow(flowId);
});

test("job posting search matches the indexed title and scopes results", async (context) => {
  const { client, jobPostingFlow, channelFlow } = environment;
  const postingId = environment.newFlowId("job-posting-search");
  const otherFlowId = environment.newFlowId("channel-search");
  const titleTerm = postingId.replaceAll("-", "");
  await client.startFlow(jobPostingFlow, postingId, undefined, {
    ...environment.startOptions(),
    attributes: [
      InitialAttribute.of(jobPostingFlow.title, "Software Engineer"),
      InitialAttribute.of(jobPostingFlow.jobDescription, "Build reliable systems"),
      InitialAttribute.of(jobPostingFlow.lastUpdateTimeMillis, BigInt(Date.now())),
      InitialAttribute.of(jobPostingFlow.updateVersion, 0),
    ],
  });
  context.after(() => client.stopFlow(postingId));
  await client.waitForStepCompletion(
    postingId,
    StepExecutionId.of("InitStep", 1),
    { requestTimeoutMs: 30_000 },
  );
  await client.invokeRPC(jobPostingFlow.update, postingId, {
    title: `${titleTerm} Engineer`,
    description: "Build reliable systems",
    notes: "indexed title",
  });
  await client.startFlow(channelFlow, otherFlowId, 3600, environment.startOptions());
  context.after(() => client.stopFlow(otherFlowId));

  const server = express().use("/products/job-post", createJobPostingRouter(client))
    .listen(0, "127.0.0.1");
  context.after(() => new Promise<void>((resolve) => {
    server.closeAllConnections();
    server.close(() => resolve());
  }));
  await once(server, "listening");
  const searchUrl = new URL(`http://127.0.0.1:${(server.address() as AddressInfo).port}/products/job-post/search`);
  const titleQuery = `text1 = '${titleTerm}'`;
  const mixedQuery = `${titleQuery} OR WorkflowId = '${otherFlowId}'`;
  await awaitCondition(
    async () => (await client.searchFlows(mixedQuery, 20)).flows.map((flow) => flow.flowId),
    (ids) => ids.includes(postingId) && ids.includes(otherFlowId),
    20_000,
    "Both Flows must be searchable before checking the router scope",
  );

  assert.deepEqual(await searchJobPostingIds(searchUrl, titleQuery), [postingId]);
  assert.deepEqual(await searchJobPostingIds(searchUrl, mixedQuery), [postingId]);
  assert.deepEqual(await searchJobPostingIds(searchUrl, `text1 = '${titleTerm}missing'`), []);
});

async function searchJobPostingIds(searchUrl: URL, query: string): Promise<string[]> {
  const url = new URL(searchUrl);
  url.searchParams.set("query", query);
  const response = await fetch(url);
  assert.equal(response.status, 200);
  const body = await response.json() as { flowIDs: string[] };
  return body.flowIDs;
}
