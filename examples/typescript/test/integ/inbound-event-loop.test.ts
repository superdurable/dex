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

import {
  CURRENT_TRANSCRIPT_CHUNK,
  MAX_REPLY_LENGTH,
  supportTicketFlow,
  transcriptChunks,
} from "../../src/patterns/inbound-event-loop/support-ticket-flow.js";
import {
  acquireIntegEnvironment,
  awaitCondition,
  releaseIntegEnvironment,
  type IntegEnvironment,
} from "./environment.js";

const WAIT_TIMEOUT_MS = 45_000;

let environment: IntegEnvironment;

test.before(async () => {
  environment = await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

async function startSupportTicket(prefix: string): Promise<string> {
  const flowId = environment.newFlowId(prefix);
  await environment.client.startFlow(supportTicketFlow, flowId, "", environment.startOptions());
  return flowId;
}

async function transcriptTexts(flowId: string): Promise<string[]> {
  const [chunk] = await environment.client.getAttributes(
    flowId,
    transcriptChunks.instance(CURRENT_TRANSCRIPT_CHUNK),
  );
  return (chunk?.replies ?? []).map((reply) => reply.text);
}

async function completedOutput(flowId: string): Promise<string> {
  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", `${flowId}: ${result.errorMessage ?? ""}`);
  assert.equal(result.completions.length, 1);
  return result.completions[0]!.decode(stringCodec);
}

test("inbound event loop records replies before the resolution", async () => {
  const flowId = await startSupportTicket("inbound-event-loop");

  await assert.rejects(
    environment.client.invokeRPC(supportTicketFlow.sendTicketReply, flowId, "  "),
    /reply text is required/,
  );
  await assert.rejects(
    environment.client.invokeRPC(supportTicketFlow.resolveTicket, flowId, " "),
    /resolution reason is required/,
  );

  await environment.client.invokeRPC(supportTicketFlow.sendTicketReply, flowId, "printer is offline");
  await environment.client.invokeRPC(
    supportTicketFlow.sendTicketReply,
    flowId,
    "restarted it, still offline",
  );
  await environment.client.invokeRPC(
    supportTicketFlow.resolveTicket,
    flowId,
    "replaced the network cable",
  );

  assert.equal(await completedOutput(flowId), "resolved: replaced the network cable");
  assert.deepEqual(await transcriptTexts(flowId), [
    "printer is offline",
    "restarted it, still offline",
  ]);
});

test("inbound event loop counts reply length in characters", async () => {
  const flowId = await startSupportTicket("inbound-event-loop-length");
  // Each emoji is one character but four UTF-8 bytes and two UTF-16 code units.
  const longestReply = "😀".repeat(MAX_REPLY_LENGTH);

  await assert.rejects(
    environment.client.invokeRPC(supportTicketFlow.sendTicketReply, flowId, `${longestReply}😀`),
    /reply text exceeds 4000 characters/,
  );
  await environment.client.invokeRPC(supportTicketFlow.sendTicketReply, flowId, longestReply);
  await environment.client.invokeRPC(supportTicketFlow.resolveTicket, flowId, "answered");

  assert.equal(await completedOutput(flowId), "resolved: answered");
  assert.deepEqual(await transcriptTexts(flowId), [longestReply]);
});

test("inbound event loop closes a quiet ticket", async () => {
  const flowId = await startSupportTicket("inbound-event-loop-inactive");
  await environment.client.invokeRPC(supportTicketFlow.sendTicketReply, flowId, "is anyone there?");
  await awaitCondition(
    () => transcriptTexts(flowId),
    (texts) => texts.length === 1 && texts[0] === "is anyone there?",
    WAIT_TIMEOUT_MS,
    `the reply of ${flowId} was never recorded`,
  );

  // The reply ended execution 1, so execution 2 holds the inactivity Timer.
  await awaitCondition(
    async () => {
      try {
        await environment.client.skipTimer(
          flowId,
          StepExecutionId.of("AwaitTicketMessage", 2),
          TimerId.byConditionIndex(0),
        );
        return true;
      } catch {
        return false;
      }
    },
    (isSkipped) => isSkipped,
    WAIT_TIMEOUT_MS,
    `the inactivity Timer of ${flowId} was never skipped`,
  );

  assert.equal(await completedOutput(flowId), "closed after inactivity");
});
