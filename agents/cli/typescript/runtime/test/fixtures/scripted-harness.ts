// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { existsSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

import {
  ScriptedModelClient,
  createStepwiseAgentSessionFlow,
  functionTool,
  type FunctionTool,
  type StepwiseAgentRunner,
  type ModelClient,
  type ModelRequest,
  type ScriptedModelReply,
} from "@superdurable/dex-agents/stepwise";

import type { AgentSessionFlow } from "@superdurable/dex-agents";

import { isLocalToolGroupEnabled, type AgentHarness, type HarnessContext } from "../../src/index.js";

/** Environment variable naming the directory where the scripted model writes gate markers. */
export const scriptedHarnessDirectoryVariable = "DEX_AGENTS_CLI_TEST_DIRECTORY";
/**
 * Environment variable that selects `shell-replacing` for {@link shellReplacingScriptedHarness} or `fixed-tool-set`
 * for {@link fixedToolSetScriptedHarness}.
 */
export const scriptedHarnessVariantVariable = "DEX_AGENTS_CLI_TEST_HARNESS";

/**
 * A Stepwise test harness whose model follows commands in the user's text:
 *
 * - `tools`: answers with the offered tool names.
 * - `model`: answers with the turn's model.
 * - `instructions`: answers with the turn's instructions.
 * - `history`: answers with every user message of the session.
 * - `call <tool> <json>`: calls the tool, then answers `result: <output>`.
 * - `wait <gate>`: writes `<gate>.reached` and waits for `<gate>.release` or cancellation.
 * - anything else: answers `echo: <text>`.
 */
export const scriptedHarness: AgentHarness = {
  name: "scripted",
  configSection: "builtin",
  flowType: "DexAgentsCliScriptedSession",
  defaultModel: "scripted/default",
  createSessionFlow: (context: HarnessContext) => createScriptedSessionFlow(context, []),
};

/** The scripted harness declaring that its runner fixes its tools when the Flow is built. */
export const fixedToolSetScriptedHarness: AgentHarness = { ...scriptedHarness, isToolSetFixedAtBuild: true };

/**
 * The scripted harness with the `shell` group replaced by its own `replacement_shell` function tool, which answers
 * with the permissions it reads in its tool Step through `resolveToolStepPermissions`.
 */
export const shellReplacingScriptedHarness: AgentHarness = {
  ...scriptedHarness,
  flowType: "DexAgentsCliScriptedShellSession",
  replacedLocalTools: ["shell"],
  createSessionFlow: (context: HarnessContext) =>
    createScriptedSessionFlow(context, [
      functionTool<Record<string, never>>({
        name: "replacement_shell",
        description: "Report the shell permissions of this tool Step.",
        parameters: { type: "object", properties: {}, additionalProperties: false },
        effect: "read",
        execute: async (_arguments, call) => {
          const permissions = await context.resolveToolStepPermissions(call);
          return JSON.stringify({
            enabled: isLocalToolGroupEnabled(permissions, "shell"),
            allow: permissions.local_tools.shell.allow,
          });
        },
      }),
    ]),
};

function createScriptedSessionFlow(
  context: HarnessContext,
  tools: readonly FunctionTool[],
): AgentSessionFlow<StepwiseAgentRunner> {
  const models = new Map<string, ModelClient>();
  const modelFor = (model: string): ModelClient => {
    let client = models.get(model);
    if (client === undefined) {
      client = new ScriptedModelClient((request) => respond(request, model));
      models.set(model, client);
    }
    return client;
  };
  return createStepwiseAgentSessionFlow(
    {
      name: "scripted",
      instructions: (turn) => context.resolveTurn(turn.sessionSettings ?? {}).instructions ?? "You are a scripted test agent.",
      model: modelFor(context.config.model),
      resolveModel: (settings) => modelFor(context.resolveTurn(settings).model),
      toolSources: context.toolSources,
      tools,
    },
    context.flowType,
  );
}

async function respond(request: ModelRequest, model: string): Promise<ScriptedModelReply> {
  const text = (ScriptedModelClient.userTexts(request).at(-1) ?? "").trim();
  const results = ScriptedModelClient.toolResults(request);
  if (text === "tools") {
    return { text: `tools: ${request.tools.map((tool) => tool.name).sort().join(",")}` };
  }
  if (text === "model") {
    return { text: `model: ${model}` };
  }
  if (text === "instructions") {
    return { text: `instructions: ${request.instructions}` };
  }
  if (text === "history") {
    return { text: `history: ${ScriptedModelClient.userTexts(request).join(" | ")}` };
  }
  const call = /^call (\S+)(?: (.*))?$/s.exec(text);
  if (call !== null) {
    const lastResult = results.at(-1);
    if (lastResult !== undefined) {
      return { text: `result: ${lastResult.output}` };
    }
    return { toolCalls: [{ toolName: call[1] ?? "", arguments: call[2] ?? "{}" }] };
  }
  const wait = /^wait (\S+)$/.exec(text);
  if (wait !== null) {
    const directory = process.env[scriptedHarnessDirectoryVariable] ?? ".";
    writeFileSync(join(directory, `${wait[1]}.reached`), "");
    while (!existsSync(join(directory, `${wait[1]}.release`))) {
      if (request.cancellationSignal.aborted) {
        throw new Error("the model call was cancelled");
      }
      await delay(100);
    }
    return { text: `released ${wait[1]}` };
  }
  return { text: `echo: ${text}` };
}
