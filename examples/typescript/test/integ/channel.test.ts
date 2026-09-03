/*
 * Copyright (c) 2026 Super Durable, Inc.
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

import { ChannelMessageNotFoundError } from "@superdurable/dex";

import { moved, queued } from "../../src/primitives/channel/channel-flow.js";
import {
  acquireIntegEnvironment,
  releaseIntegEnvironment,
} from "./environment.js";

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("channel message can be moved by ID", async () => {
  const environment = await acquireIntegEnvironment();
  const flowId = environment.newFlowId("channel-message");
  await environment.client.startFlow(
    environment.channelFlow,
    flowId,
    30,
    environment.startOptions(),
  );
  await environment.client.publish(flowId, queued, "delete me");
  await environment.client.publish(flowId, queued, "move me");

  const pending = await environment.client.getChannelMessages(flowId, queued);
  assert.deepEqual(pending.map((message) => message.value), ["delete me", "move me"]);
  await environment.client.deleteChannelMessage(flowId, queued, pending[0]!.messageId);

  const move = { messageId: pending[1]!.messageId };
  await environment.client.invokeRPC(environment.channelFlow.move, flowId, move);
  assert.deepEqual(
    (await environment.client.getChannelMessages(flowId, moved)).map((message) => message.value),
    ["move me"],
  );

  await assert.rejects(
    environment.client.invokeRPC(environment.channelFlow.move, flowId, move),
    ChannelMessageNotFoundError,
  );
  assert.deepEqual(
    (await environment.client.getChannelMessages(flowId, moved)).map((message) => message.value),
    ["move me"],
  );

  await environment.client.invokeRPC(environment.channelFlow.approve, flowId);
});
