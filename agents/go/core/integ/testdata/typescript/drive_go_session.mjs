// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Reads a Go-served session's snapshot with the TypeScript core's Client, approves its write,
// waits for the turn, and prints what it saw as JSON.
// Usage: node drive_go_session.mjs <TypeScript core directory> <server address> <session ID>

import { pathToFileURL } from "node:url";
import { join } from "node:path";

const [coreDirectory, serverAddress, sessionId] = process.argv.slice(2);
const load = (path) => import(pathToFileURL(join(coreDirectory, "dist", "src", path)).href);
const { createStepwiseAgentSessionFlow } = await load("stepwise/index.js");
const { AgentSessionTestClient, AgentTestEnvironment } = await load("testing/index.js");

// The Flow only types the Client calls; the Go Worker runs the Steps.
const flow = createStepwiseAgentSessionFlow(
  {
    name: "typing-only",
    instructions: "unused",
    model: {
      callModel: async () => {
        throw new Error("the typing-only Flow never calls a model");
      },
      estimateInputTokens: () => 0,
    },
  },
  "ScriptedAgent",
);
const environment = await AgentTestEnvironment.start({ serverAddress, flows: [flow], isWorkerInProcess: false });
try {
  const sessions = new AgentSessionTestClient(environment.client, flow);
  const waiting = await sessions.getAgentSnapshot(sessionId);
  const interrupt = waiting.pendingInterrupts[0];
  const isResolved = await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true);
  const finished = await sessions.waitForCompletedTurns(sessionId, 1);
  const done = await sessions.getAgentSnapshot(sessionId);
  const page = await sessions.listAgentMessages(sessionId, null, 2);
  console.log(
    JSON.stringify({
      waitingStatus: waiting.state.status,
      waitingToolCalls: waiting.toolCalls.map((toolCall) => [toolCall.toolName, toolCall.status, toolCall.effect]),
      interrupt: [interrupt.kind, interrupt.toolName, interrupt.choices, interrupt.allowsFreeText],
      isResolved,
      turnCount: finished.turnCount,
      acceptsTurnControl: done.state.acceptsTurnControl ?? false,
      interactionMode: done.state.interactionMode ?? "chat",
      messageTexts: done.messages.map((message) => message.text),
      usage: done.usage,
      pageSequences: page.messages.map((message) => message.sequence),
    }),
  );
} finally {
  await environment.close();
}
