// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import type { CompactionPolicy, ToolPolicy } from "@superdurable/dex-agents";
import type { ModelClient, ModelRequest } from "@superdurable/dex-agents/builtin-loop";
import { createBuiltinAgentSessionFlow } from "@superdurable/dex-agents/builtin-loop";
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

/** Characters added to the result of an order ID containing `huge`, to push a session over its window. */
export const hugeOrderPaddingCharacters = 80_000;

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
  readonly compaction?: CompactionPolicy;
  /** Read on every Anthropic call: `undefined` keeps `medium`, `null` sends no `reasoningEffort`. */
  readonly anthropicReasoningEffort?: () => string | null | undefined;
  readonly toolPolicies?: Readonly<Record<string, ToolPolicy>>;
}

/** A built-in loop agent with Pi tools; session settings `model` picks `openai` (default) or `anthropic`. */
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
    return withReasoningEffortOverride(model, options.anthropicReasoningEffort);
  };
  const tools = new PiTestTools(options.markerDirectory);
  return createBuiltinAgentSessionFlow(
    {
      name: "pi-test",
      instructions: "You are the Pi test agent.",
      model: openAiModel,
      resolveModel: (settings) => (settings["model"] === "anthropic" ? resolveAnthropicModel() : openAiModel),
      modelSettings: { reasoningEffort: "medium" },
      ...(options.compaction === undefined ? {} : { compaction: options.compaction }),
      ...(options.toolPolicies === undefined ? {} : { toolPolicies: options.toolPolicies }),
      tools: [
        piAgentFunctionTool(tools.lookupOrder()),
        piAgentFunctionTool(tools.getWeather(), { effect: "read" }),
        piAgentFunctionTool(tools.failLookup(), { effect: "read" }),
        piAgentFunctionTool(tools.sendEmail()),
        piAgentFunctionTool(tools.submitPayment()),
        piAgentFunctionTool(tools.deleteFile(), { effect: "unclassified" }),
        piAgentFunctionTool(tools.renameCustomer(), { effect: "read" }),
        piDurableFunctionTool(tools.durableForecast()),
        piDurableFunctionTool(tools.durableCharge()),
        piDurableFunctionTool(tools.durableNestedCall(), { effect: "read" }),
        piDurableFunctionTool(tools.durableFinishTurn(), { effect: "read" }),
        piDurableFunctionTool(tools.durableFailLookup(), { effect: "read" }),
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
        const padding = params.order_id.includes("huge") ? ` ${"x".repeat(hugeOrderPaddingCharacters)}` : "";
        return textResult(`order ${params.order_id} shipped${padding}`);
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
      execute: async (toolCallId) => {
        new InvocationLog(join(this.markerDirectory, "fail_lookup.log")).record(toolCallId);
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

  public submitPayment(): PiAgentToolLike {
    return {
      name: "submit_payment",
      description: "Submit a payment; `throw` throws, `reject` reports a failure.",
      parameters: { type: "object", properties: { outcome: { type: "string" } }, required: ["outcome"] },
      replay: "never",
      execute: async (toolCallId, params: { outcome: string }) => {
        new InvocationLog(join(this.markerDirectory, "submit_payment.log")).record(`${params.outcome} ${toolCallId}`);
        if (params.outcome.startsWith("throw")) {
          throw new Error("payment gateway timed out");
        }
        return { content: [{ type: "text", text: "card declined" }], details: undefined, isError: true };
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
      parameters: {
        type: "object",
        properties: { name: { type: "string" } },
        required: ["name"],
        additionalProperties: false,
      },
      // Version 1 took { old_name }; { legacy_id } was never supported.
      prepareArguments: (args) => {
        const raw = args as { name?: unknown; old_name?: unknown; legacy_id?: unknown };
        if (raw.legacy_id !== undefined) {
          throw new Error("legacy_id is no longer supported");
        }
        return raw.name === undefined && raw.old_name !== undefined ? { name: raw.old_name } : raw;
      },
      execute: async (toolCallId, params: { name: string }) => {
        new InvocationLog(join(this.markerDirectory, "rename_customer.log")).record(`${params.name} ${toolCallId}`);
        return textResult(`renamed to ${params.name}`);
      },
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

  public durableFailLookup(): PiDurableToolLike {
    return {
      name: "durable_fail_lookup",
      description: "A pi-durable lookup that fails.",
      parameters: { type: "object", properties: { key: { type: "string" } }, required: ["key"] },
      execute: async (args: { key: string }) => {
        new InvocationLog(join(this.markerDirectory, "durable_fail_lookup.log")).record(args.key);
        throw new Error("archive offline");
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

/** A client that replaces the request's `reasoningEffort` on each call when `override` returns a value. */
function withReasoningEffortOverride(
  model: ModelClient,
  override: (() => string | null | undefined) | undefined,
): ModelClient {
  if (override === undefined) {
    return model;
  }
  const overrideSettings = (request: ModelRequest): ModelRequest => {
    const reasoningEffort = override();
    if (reasoningEffort === undefined) {
      return request;
    }
    const { reasoningEffort: _replaced, ...settings } = request.settings;
    return { ...request, settings: reasoningEffort === null ? settings : { ...settings, reasoningEffort } };
  };
  return {
    callModel: (request, output) => model.callModel(overrideSettings(request), output),
    estimateInputTokens: (request) => model.estimateInputTokens(overrideSettings(request)),
  };
}

function textResult(text: string): PiAgentToolResultLike {
  return { content: [{ type: "text", text }], details: undefined };
}
