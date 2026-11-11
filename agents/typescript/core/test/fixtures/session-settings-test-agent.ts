// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import type { AgentSessionFlow, SessionSettings } from "../../src/index.js";
import {
  ScriptedModelClient,
  BuiltinLoopAgentRunner,
  createBuiltinAgentSessionFlow,
  functionTool,
  type ModelRequest,
  type ScriptedModelReply,
} from "../../src/builtin-loop/index.js";
import { FileGate, InvocationLog } from "../../src/testing/index.js";

/** Flow type of the session settings test agent. */
export const sessionSettingsTestAgentFlowType = "SessionSettingsTestAgent";
/** Environment variable naming the marker directory of a Worker child. */
export const sessionSettingsMarkerDirectoryVariable = "SESSION_SETTINGS_AGENT_MARKER_DIRECTORY";
/** Base instructions; a `persona` setting appends `Persona: <persona>.`. */
export const sessionSettingsTestInstructions = "You are the settings test agent.";
const modelNames = ["default", "alpha", "beta"] as const;

/**
 * Creates a Stepwise agent whose `resolveModel` picks the scripted model named by the settings' `model`, or
 * `default`. Every model call records `<flowId> <turnId> <round> <name>` in `models.log`. The user text
 * `which-model` answers `model <name>: <instructions>`. `lookup <query>` calls `lookup_order`, which waits at
 * the `lookup` FileGate for a query that starts with `gated`; `lookup-then-stall <query>` also waits at the
 * `model` FileGate in the second model call. After the tool, the model answers `lookup_order: <output>`.
 */
export function createSessionSettingsTestAgentFlow(markerDirectory: string): AgentSessionFlow<BuiltinLoopAgentRunner> {
  const modelLog = new InvocationLog(join(markerDirectory, "models.log"));
  const waitAtGate = async (gateName: string, signal?: AbortSignal) => {
    const gate = new FileGate(markerDirectory, gateName);
    gate.markReached();
    await gate.waitForRelease(signal);
  };
  const respond = async (modelName: string, request: ModelRequest): Promise<ScriptedModelReply> => {
    const command = ScriptedModelClient.userTexts(request).at(-1) ?? "";
    const results = ScriptedModelClient.toolResults(request);
    modelLog.record(`${request.metadata.flowId} ${request.metadata.turnId} ${results.length + 1} ${modelName}`);
    const [verb, query = ""] = command.split(" ", 2);
    if (verb === "which-model") {
      return { text: `model ${modelName}: ${request.instructions}` };
    }
    if (results.length > 0) {
      if (verb === "lookup-then-stall") {
        await waitAtGate("model", request.cancellationSignal);
      }
      return { text: results.map((result) => `${result.toolName}: ${result.output}`).join("; ") };
    }
    if (verb === "say") {
      return { text: `said: ${query}` };
    }
    return { toolCalls: [{ toolName: "lookup_order", arguments: { query } }] };
  };
  const models = Object.fromEntries(
    modelNames.map((name) => [name, new ScriptedModelClient((request) => respond(name, request))]),
  ) as Record<(typeof modelNames)[number], ScriptedModelClient>;
  return createBuiltinAgentSessionFlow(
    {
      name: "settings-test",
      instructions: ({ sessionSettings }) => {
        const persona = sessionSettings?.["persona"];
        return persona === undefined
          ? sessionSettingsTestInstructions
          : `${sessionSettingsTestInstructions} Persona: ${String(persona)}.`;
      },
      model: models.default,
      resolveModel: (sessionSettings: SessionSettings) =>
        models[String(sessionSettings["model"] ?? "default") as (typeof modelNames)[number]],
      tools: [
        functionTool<{ query: string }>({
          name: "lookup_order",
          description: "Look up an order.",
          parameters: {
            type: "object",
            properties: { query: { type: "string" } },
            required: ["query"],
            additionalProperties: false,
          },
          effect: "read",
          execute: async ({ query }, context) => {
            if (query.startsWith("gated")) {
              await waitAtGate("lookup", context.cancellationSignal);
            }
            return `found ${query}`;
          },
        }),
      ],
    },
    sessionSettingsTestAgentFlowType,
  );
}

/** Returns the model names that served one session, as `<turnId> <round> <name>`, in call order. */
export function readModelCalls(markerDirectory: string, sessionId: string): string[] {
  return new InvocationLog(join(markerDirectory, "models.log"))
    .names()
    .filter((line) => line.startsWith(`${sessionId} `))
    .map((line) => line.slice(sessionId.length + 1));
}
