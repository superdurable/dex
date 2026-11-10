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

import { appendFileSync } from "node:fs";

import { newConnectionWithOperations, type Connection } from "@superdurable/connector-slack";
import { GeneratedMock } from "@superdurable/connector-slack/mock";
import { runAgentWorkerProcess } from "@superdurable/dex-agents/testing";

import { loadAiAgentConfig } from "../../src/products/ai-agent/ai-agent-config.js";
import { createAiAgentFlow } from "../../src/products/ai-agent/ai-agent-flow.js";
import { slackConnectionName } from "../../src/shared/slack-connector-tools.js";

// The AI Agent Worker as a child process, so a test can kill it mid-turn.
await runAgentWorkerProcess([createAiAgentFlow(loadAiAgentConfig(), openRecordingSlackMock())]);

/** The Slack connector mock, recording every post to a file because the test runs in another process. */
function openRecordingSlackMock(): Connection | undefined {
  // ai-agent.test.ts sets it; each line is one mock call as JSON.
  const callsFile = process.env.AI_AGENT_TEST_SLACK_CALLS_FILE;
  if (callsFile === undefined) {
    return undefined;
  }
  const slackMock = new GeneratedMock(slackConnectionName);
  const mockPostThreadReply = slackMock.postThreadReply();
  return newConnectionWithOperations(
    {
      listThreadMessages: () => slackMock.listThreadMessages(),
      getThreadReply: () => slackMock.getThreadReply(),
      postChannelMessage: () => slackMock.postChannelMessage(),
      postThreadReply: () => ({
        definition: () => mockPostThreadReply.definition(),
        idempotencyKey: (callId: string) => mockPostThreadReply.idempotencyKey(callId),
        invoke: (call, input) => {
          const attempt = mockPostThreadReply.invoke(call, input);
          appendFileSync(callsFile, `${JSON.stringify(mockPostThreadReply.calls().at(-1))}\n`);
          return attempt;
        },
      }),
    },
    slackMock.connection().reference,
  );
}
