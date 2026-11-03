// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// It imports nothing from the agents core, so a released 0.1.0 Worker can load it next to its own core.
import { existsSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

/** Flow type of the message chunks test agent. */
export const messageChunksAgentFlowType = "MessageChunksAgent";
/** Environment variable naming the marker directory of a Worker child. */
export const messageChunksMarkerDirectoryVariable = "MESSAGE_CHUNKS_AGENT_STATE_DIR";
/** Environment variable that sets `messageWindowSize`. */
export const messageWindowSizeVariable = "MESSAGE_CHUNKS_AGENT_WINDOW_SIZE";
/** Environment variable that sets `messageChunkSize`. */
export const messageChunkSizeVariable = "MESSAGE_CHUNKS_AGENT_CHUNK_SIZE";
/** Environment variable that sets `messageChunkMaxBytes`. */
export const messageChunkMaxBytesVariable = "MESSAGE_CHUNKS_AGENT_CHUNK_MAX_BYTES";
/** Marker a model call writes before it waits at the `model` gate. */
export const modelGateReachedFileName = "model.reached";
/** Marker that releases the `model` gate. */
export const modelGateReleasedFileName = "model.released";

interface EchoMessage {
  readonly role: "user" | "assistant" | "tool";
  readonly text: string | null;
  readonly toolCallId: null;
  readonly toolName: null;
  readonly toolArguments: null;
  readonly toolOutput: null;
  readonly frameworkItem: string;
}

interface EchoTurn {
  readonly messages: readonly { readonly sequence: number; readonly role: string; readonly text: string | null }[];
}

/**
 * A runner whose model echoes the turn's last user text and the sequences it received, such as
 * `echo: hello | saw 1-3`. A user text containing `gate` waits at the `model` gate until it is released, and
 * `big <n>` answers with exactly n "y" characters.
 * It needs only the runner methods of 0.1.0, so the released core runs it too.
 */
export class EchoRunner {
  public constructor(private readonly markerDirectory: string) {}

  public buildUserMessage(text: string): EchoMessage {
    return echoMessage("user", text);
  }

  public async runModelStep(turn: EchoTurn): Promise<unknown> {
    const lastUser = [...turn.messages].reverse().find((message) => message.role === "user");
    const text = lastUser?.text ?? "";
    if (text.includes("gate")) {
      writeFileSync(join(this.markerDirectory, modelGateReachedFileName), "");
      while (!existsSync(join(this.markerDirectory, modelGateReleasedFileName))) {
        await delay(25);
      }
    }
    const bigReply = /^big (\d+)$/.exec(text);
    if (bigReply !== null) {
      // An answer of exactly that many "y" characters, so a test can size the stored message.
      return {
        kind: "completed",
        newMessages: [echoMessage("assistant", "y".repeat(Number(bigReply[1])))],
        usage: { modelCalls: 1, inputTokens: 1, outputTokens: 1, cachedInputTokens: 0, reasoningTokens: 0 },
      };
    }
    const first = turn.messages[0]?.sequence ?? 0;
    const last = turn.messages.at(-1)?.sequence ?? 0;
    return {
      kind: "completed",
      newMessages: [echoMessage("assistant", `echo: ${text} | saw ${first}-${last} (${turn.messages.length})`)],
      usage: { modelCalls: 1, inputTokens: 1, outputTokens: 1, cachedInputTokens: 0, reasoningTokens: 0 },
    };
  }

  public executeDeferredToolCall(): Promise<never> {
    return Promise.reject(new Error("the echo agent has no tools"));
  }
}

function echoMessage(role: "user" | "assistant", text: string): EchoMessage {
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
