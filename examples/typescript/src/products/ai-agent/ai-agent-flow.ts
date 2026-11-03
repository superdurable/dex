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

import { openai } from "@ai-sdk/openai";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createAiSdkModelClient } from "@superdurable/dex-agents/ai-sdk";
import { McpToolSource } from "@superdurable/dex-agents/mcp";
import {
  createStepwiseAgentSessionFlow,
  type ModelClient,
  type StepwiseAgentRunner,
} from "@superdurable/dex-agents/stepwise";

import type { AiAgentConfig } from "./ai-agent-config.js";
import { createScriptedModelClient } from "./scripted-model.js";

export type AiAgentFlow = AgentSessionFlow<StepwiseAgentRunner>;

/**
 * Builds the AI Agent session Flow from Worker configuration. Each session is one Flow execution,
 * and the library owns its Steps, state, RPCs, and events.
 */
export function createAiAgentFlow(config: AiAgentConfig): AiAgentFlow {
  const clients = new Map(config.models.map((model) => [model, createModelClient(model)]));
  const defaultClient = clients.get(config.models[0]!)!;
  return createStepwiseAgentSessionFlow(
    {
      name: "ai-agent",
      instructions: config.systemPrompt,
      model: defaultClient,
      // Settings hold only a model name; an unknown one gets the default, never a new client.
      resolveModel: (settings) => clients.get(String(settings.model)) ?? defaultClient,
      // Each MCP tool's annotations pick its effect, and so whether it waits for approval.
      toolSources: [new McpToolSource(config.mcpServers, { resourceAndPromptToolsEnabled: true })],
      builtInTools: ["ask_user", "durable_wait", "write_todos"],
      planModeEnabled: true,
      compaction: { contextWindowTokens: config.contextWindowTokens },
    },
    "AIAgentSession",
  );
}

function createModelClient(model: string): ModelClient {
  if (model.startsWith("mock/")) {
    return createScriptedModelClient(model);
  }
  if (!model.startsWith("openai/")) {
    throw new RangeError(`an AI Agent model must be mock/<name> or openai/<model>, not ${model}`);
  }
  // The AI SDK reads OPENAI_API_KEY from the Worker's environment, never from Flow state.
  return createAiSdkModelClient(openai(model.slice("openai/".length)));
}
