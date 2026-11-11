// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import type { ModelClient } from "@superdurable/dex-agents/stepwise";
import { createStepwiseAgentSessionFlow } from "@superdurable/dex-agents/stepwise";
import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";

import {
  createPiAiModelClient,
  piAgentFunctionTool,
  piDurableFunctionTool,
  type PiAgentToolLike,
  type PiAgentToolResultLike,
  type PiAiModelClientOptions,
  type PiDurableToolLike,
} from "../../src/index.js";
import { createFakeAnthropicModel, createFakeOpenAiModel } from "./fake-model-server.js";

/** Environment variable naming the fake provider's base URL for a Worker process. */
export const piTestModelServerVariable = "PI_TEST_MODEL_SERVER";
/** Environment variable naming the marker directory for a Worker process. */
export const piTestMarkerDirectoryVariable = "PI_TEST_MARKER_DIRECTORY";

/** Flow type of the agent that the Worker process registers. */
export const piWorkerKillFlowType = "PiWorkerKillAgent";

/** Session settings `model` values the test agent understands. */
export type PiTestModelName = "openai" | "anthropic";

/** Options of {@link createPiTestAgentFlow}. */
export interface PiTestAgentOptions {
  readonly flowType: string;
  readonly modelServerBaseUrl: string;
  readonly markerDirectory: string;
  /** Model ID of the Anthropic model for each call, read on every call. */
  readonly anthropicModelId?: () => string;
  readonly prepareRequest?: PiAiModelClientOptions["prepareRequest"];
}

/** A Stepwise agent with Pi tools; session settings `model` picks `openai` (default) or `anthropic`. */
export function createPiTestAgentFlow(options: PiTestAgentOptions) {
  const clientOptions: PiAiModelClientOptions = {
    apiKey: "test-key",
    ...(options.prepareRequest === undefined ? {} : { prepareRequest: options.prepareRequest }),
  };
  const openAiModel = createPiAiModelClient(createFakeOpenAiModel(options.modelServerBaseUrl), clientOptions);
  const anthropicModels = new Map<string, ModelClient>();
  const resolveAnthropicModel = (): ModelClient => {
    const modelId = options.anthropicModelId?.() ?? "claude-fake-1";
    let model = anthropicModels.get(modelId);
    if (model === undefined) {
      model = createPiAiModelClient(createFakeAnthropicModel(options.modelServerBaseUrl, modelId), {
        ...clientOptions,
      });
      anthropicModels.set(modelId, model);
    }
    return model;
  };
  const tools = new PiTestTools(options.markerDirectory);
  return createStepwiseAgentSessionFlow(
    {
      name: "pi-test",
      instructions: "You are the Pi test agent.",
      model: openAiModel,
      resolveModel: (settings) => (settings["model"] === "anthropic" ? resolveAnthropicModel() : openAiModel),
      modelSettings: { reasoningEffort: "medium" },
      tools: [
        piAgentFunctionTool(tools.lookupOrder()),
        piAgentFunctionTool(tools.getWeather(), { effect: "read" }),
        piAgentFunctionTool(tools.failLookup(), { effect: "read" }),
        piAgentFunctionTool(tools.sendEmail()),
        piAgentFunctionTool(tools.deleteFile(), { effect: "unclassified" }),
        piAgentFunctionTool(tools.renameCustomer(), { effect: "read" }),
        piDurableFunctionTool(tools.durableForecast()),
        piDurableFunctionTool(tools.durableCharge()),
        piDurableFunctionTool(tools.durableNestedCall(), { effect: "read" }),
        piDurableFunctionTool(tools.durableFinishTurn(), { effect: "read" }),
      ],
    },
    options.flowType,
  );
}

/** Pi tools that log each run and wait on the gate named by an argument starting with `gated`. */
class PiTestTools {
  public constructor(private readonly markerDirectory: string) {}

  public lookupOrder(): PiAgentToolLike {
    return {
      name: "lookup_order",
      description: "Look up an order.",
      parameters: { type: "object", properties: { order_id: { type: "string" } }, required: ["order_id"] },
      replay: "safe",
      execute: async (toolCallId, params: { order_id: string }, signal, onUpdate) => {
        await this.recordAndWait("lookup_order", toolCallId, params.order_id, signal);
        onUpdate?.(textResult(`looking up ${params.order_id}`));
        return textResult(`order ${params.order_id} shipped`);
      },
    };
  }

  public getWeather(): PiAgentToolLike {
    return {
      name: "get_weather",
      description: "Get the weather.",
      parameters: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
      execute: async (toolCallId, params: { city: string }, signal) => {
        await this.recordAndWait("get_weather", toolCallId, params.city, signal);
        return {
          content: [
            { type: "text", text: `sunny in ${params.city}` },
            { type: "image", data: "iVBORw0KGgo=", mimeType: "image/png" },
          ],
          details: { city: params.city },
        };
      },
    };
  }

  public failLookup(): PiAgentToolLike {
    return {
      name: "fail_lookup",
      description: "A lookup that fails.",
      parameters: { type: "object", properties: {} },
      execute: async () => {
        throw new Error("warehouse offline");
      },
    };
  }

  public sendEmail(): PiAgentToolLike {
    return {
      name: "send_email",
      description: "Send an email.",
      parameters: { type: "object", properties: { to: { type: "string" } }, required: ["to"] },
      replay: "never",
      execute: async (toolCallId, params: { to: string }, signal) => {
        await this.recordAndWait("send_email", toolCallId, params.to, signal);
        return textResult(`email sent to ${params.to}`);
      },
    };
  }

  public deleteFile(): PiAgentToolLike {
    return {
      name: "delete_file",
      description: "Delete a file.",
      parameters: { type: "object", properties: { path: { type: "string" } }, required: ["path"] },
      execute: async (toolCallId, params: { path: string }, signal) => {
        await this.recordAndWait("delete_file", toolCallId, params.path, signal);
        return textResult(`deleted ${params.path}`);
      },
    };
  }

  public renameCustomer(): PiAgentToolLike {
    return {
      name: "rename_customer",
      description: "Rename a customer.",
      parameters: { type: "object", properties: { name: { type: "string" } } },
      prepareArguments: (args) => {
        const raw = args as { name?: unknown; old_name?: unknown };
        return raw.name === undefined && raw.old_name !== undefined ? { name: raw.old_name } : raw;
      },
      execute: async (_toolCallId, params: { name?: string }) => textResult(`renamed to ${String(params.name)}`),
    };
  }

  public durableForecast(): PiDurableToolLike {
    return {
      name: "durable_forecast",
      description: "Get a forecast.",
      parameters: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
      replay: "safe",
      execute: async (args: { city: string }, _api, context: { abortSignal: AbortSignal }) => {
        await this.recordAndWait("durable_forecast", "call", args.city, context.abortSignal);
        return { output: [{ type: "text", text: `forecast for ${args.city}: rain` }] };
      },
    };
  }

  public durableCharge(): PiDurableToolLike {
    return {
      name: "durable_charge",
      description: "Charge a card.",
      parameters: { type: "object", properties: { amount: { type: "string" } }, required: ["amount"] },
      execute: async (args: { amount: string }, _api, context: { abortSignal: AbortSignal }) => {
        await this.recordAndWait("durable_charge", "call", args.amount, context.abortSignal);
        return { output: [{ type: "text", text: `charged ${args.amount}` }] };
      },
    };
  }

  public durableNestedCall(): PiDurableToolLike {
    return {
      name: "durable_nested_call",
      description: "Calls another tool through the Harness.",
      parameters: { type: "object", properties: {} },
      execute: async (_args, api: { executeTool: (name: string) => Promise<unknown> }) => {
        await api.executeTool("durable_forecast");
        return { output: [{ type: "text", text: "unreachable" }] };
      },
    };
  }

  public durableFinishTurn(): PiDurableToolLike {
    return {
      name: "durable_finish_turn",
      description: "Asks the loop to stop.",
      parameters: { type: "object", properties: {} },
      execute: async () => {
        new InvocationLog(join(this.markerDirectory, "durable_finish_turn.log")).record("ran");
        return { output: [{ type: "text", text: "done" }], control: { terminate: true } };
      },
    };
  }

  private async recordAndWait(toolName: string, toolCallId: string, argument: string, signal?: AbortSignal): Promise<void> {
    new InvocationLog(join(this.markerDirectory, `${toolName}.log`)).record(`${argument} ${toolCallId}`);
    if (argument.startsWith("gated")) {
      const gate = new FileGate(this.markerDirectory, argument);
      gate.markReached();
      await gate.waitForRelease(signal);
    }
  }
}

function textResult(text: string): PiAgentToolResultLike {
  return { content: [{ type: "text", text }], details: undefined };
}
