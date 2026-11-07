// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  AgentFrameworkRunner,
  AgentTurnContext,
  AgentTurnOutcome,
  DeferredToolExecution,
  DeferredToolOutcome,
  FrameworkMessage,
} from "../../src/index.js";

const usage = { modelCalls: 1, inputTokens: 10, outputTokens: 5, cachedInputTokens: 0, reasoningTokens: 0 };

/**
 * A runner whose commands grow the session's state. `pad <n>` answers with n characters, and
 * `call <tool> <n>` runs a tool whose output has n characters, then answers `done`. `say <text>` answers `said`.
 */
export class StateLimitRunner implements AgentFrameworkRunner<DeferredToolOutcome> {
  /** Model calls this process made. */
  public modelCalls = 0;
  /** Tool runs this process made, by tool name. */
  public readonly toolRuns = new Map<string, number>();

  public buildUserMessage(text: string): FrameworkMessage {
    return textMessage("user", text);
  }

  public runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    this.modelCalls += 1;
    const lastUser = [...turn.messages].reverse().find((message) => message.role === "user");
    const [command, first, second] = (lastUser?.text ?? "").split(" ");
    if (turn.frameworkRunState !== null) {
      return Promise.resolve({ kind: "completed", newMessages: [textMessage("assistant", "done")], usage });
    }
    if (command === "call") {
      const toolName = first ?? "";
      const toolCall = {
        toolCallId: `${turn.turnId}-call`,
        toolName,
        arguments: JSON.stringify({ characters: Number(second) }),
        policy: turn.resolveToolPolicy(toolName),
      };
      return Promise.resolve({ kind: "tool_calls_requested", frameworkRunState: "{}", toolCalls: [toolCall], usage });
    }
    const text = command === "pad" ? "p".repeat(Number(first)) : "said";
    return Promise.resolve({ kind: "completed", newMessages: [textMessage("assistant", text)], usage });
  }

  public executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const { toolName, arguments: toolArguments } = call.toolCall;
    this.toolRuns.set(toolName, (this.toolRuns.get(toolName) ?? 0) + 1);
    const { characters } = JSON.parse(toolArguments) as { characters: number };
    return Promise.resolve({ output: "b".repeat(characters) });
  }
}

function textMessage(role: "user" | "assistant", text: string): FrameworkMessage {
  return {
    role,
    text,
    toolCallId: null,
    toolName: null,
    toolArguments: null,
    toolOutput: null,
    frameworkItem: JSON.stringify({ role, text }),
  };
}
