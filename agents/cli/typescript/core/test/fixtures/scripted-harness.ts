// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { AsyncLocalStorage } from "node:async_hooks";
import { spawn } from "node:child_process";
import { existsSync, writeFileSync, writeSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

import {
  ScriptedModelClient,
  BuiltinLoopAgentRunner,
  createBuiltinAgentSessionFlow,
  functionTool,
  type AgentDefinition,
  type FunctionTool,
  type ModelClient,
  type ModelRequest,
  type ScriptedModelReply,
} from "@superdurable/dex-agents/builtin-loop";

import {
  collectTurnImages,
  createAgentSessionFlow,
  loadImages,
  type AgentImageReference,
  type AgentSessionFlow,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type FrameworkMessage,
  type MediaStore,
} from "@superdurable/dex-agents";

import { isLocalToolGroupEnabled, type AgentHarness, type HarnessContext } from "../../src/index.js";

/** Environment variable naming the directory where the scripted model writes gate markers. */
export const scriptedHarnessDirectoryVariable = "DEX_AGENTS_CLI_TEST_DIRECTORY";
/**
 * Environment variable that selects a harness variant: `shell-replacing` for {@link shellReplacingScriptedHarness},
 * `fixed-tool-set` for {@link fixedToolSetScriptedHarness}, `without-images` for {@link scriptedHarnessWithoutImages},
 * `summary-on` and `summary-off` for {@link summaryForcedOnScriptedHarness} and
 * {@link summaryForcedOffScriptedHarness}, `misreported-on` and `misreported-off` for
 * {@link summaryMisreportedOnScriptedHarness} and {@link summaryMisreportedOffScriptedHarness}, or
 * `without-reasoning-effort` for {@link scriptedHarnessWithoutReasoningEffort}.
 */
export const scriptedHarnessVariantVariable = "DEX_AGENTS_CLI_TEST_HARNESS";

/** The image bytes the running model call loaded, by SHA-256, in load order. */
const loadedImages = new AsyncLocalStorage<ReadonlyMap<string, Uint8Array>>();

/**
 * A built-in loop test harness whose model follows commands in the user's text:
 *
 * - `tools`: answers with the offered tool names.
 * - `model`: answers with the turn's model.
 * - `effort`: answers with the reasoning effort of the model call's settings, or `default`.
 * - `instructions`: answers with the turn's instructions.
 * - `history`: answers with every user message of the session.
 * - `images`: answers with the session's images that the model call loaded from the media store, as
 *   `saw image <sha256> <bytes>` joined by `; `, or `saw no images`.
 * - `call <tool> <json>`: calls the tool, then answers `result: <output>`.
 * - `wait <gate>`: writes `<gate>.reached` and waits for `<gate>.release` or cancellation.
 * - `leak`: writes to fd 1 and fd 2 and runs a child that inherits them, then answers `leaked`.
 * - anything else: answers `echo: <text>`.
 *
 * Its runner takes user messages with images, as an image-capable adapter does. It declares the reasoning efforts
 * `low`, `medium`, and `high`, which its model calls carry in their settings.
 */
export const scriptedHarness: AgentHarness = {
  name: "scripted",
  configSection: "builtin",
  flowType: "DexAgentsCliScriptedSession",
  defaultModel: "scripted/default",
  supportedReasoningEfforts: ["low", "medium", "high"],
  suggestedModels: ["scripted/other", "scripted/default", "scripted/large"],
  createSessionFlow: (context: HarnessContext) => createScriptedSessionFlow(context, [], true),
};

/** The scripted harness declaring no reasoning efforts and no suggested models. */
export const scriptedHarnessWithoutReasoningEffort: AgentHarness = {
  name: scriptedHarness.name,
  configSection: scriptedHarness.configSection,
  flowType: scriptedHarness.flowType,
  defaultModel: scriptedHarness.defaultModel,
  createSessionFlow: scriptedHarness.createSessionFlow,
};

/** The scripted harness with a runner that takes no images. */
export const scriptedHarnessWithoutImages: AgentHarness = {
  ...scriptedHarness,
  flowType: "DexAgentsCliScriptedNoImagesSession",
  createSessionFlow: (context: HarnessContext) => createScriptedSessionFlow(context, [], false),
};

/** The scripted harness declaring the text chat only, as Pi does. */
export const plainOnlyScriptedHarness: AgentHarness = { ...scriptedHarness, terminalInterface: "plain" };

/** The scripted harness keeping the indexed session summary whatever `HarnessContext.isSessionSummaryIndexed` says. */
export const summaryForcedOnScriptedHarness: AgentHarness = {
  ...scriptedHarness,
  createSessionFlow: (context: HarnessContext) =>
    createScriptedSessionFlow({ ...context, isSessionSummaryIndexed: true }, [], true),
};

/** Returns the Flow behind a Proxy whose `options` report the opposite of the summary its schema declares. */
function misreportSummary(
  flow: AgentSessionFlow<BuiltinLoopAgentRunner>,
): AgentSessionFlow<BuiltinLoopAgentRunner> {
  const options = { ...flow.options, summaryAttributesIndexed: flow.options.summaryAttributesIndexed !== true };
  return new Proxy(flow, {
    get: (target, key) => (key === "options" ? options : (Reflect.get(target, key, target) as unknown)),
  });
}

/** A harness whose Flow declares the indexed session summary while its options report it off. */
export const summaryMisreportedOnScriptedHarness: AgentHarness = {
  ...scriptedHarness,
  createSessionFlow: (context: HarnessContext) =>
    misreportSummary(createScriptedSessionFlow({ ...context, isSessionSummaryIndexed: true }, [], true)),
};

/** A harness whose Flow declares no indexed session summary while its options report it on. */
export const summaryMisreportedOffScriptedHarness: AgentHarness = {
  ...scriptedHarness,
  createSessionFlow: (context: HarnessContext) =>
    misreportSummary(createScriptedSessionFlow({ ...context, isSessionSummaryIndexed: false }, [], true)),
};

/** The scripted harness leaving out the indexed session summary whatever `HarnessContext.isSessionSummaryIndexed` says. */
export const summaryForcedOffScriptedHarness: AgentHarness = {
  ...scriptedHarness,
  createSessionFlow: (context: HarnessContext) =>
    createScriptedSessionFlow({ ...context, isSessionSummaryIndexed: false }, [], true),
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
    createScriptedSessionFlow(
      context,
      [
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
      ],
      true,
    ),
};

/** The built-in loop runner with image support: each model call loads the session's images first. */
class ImageScriptedRunner extends BuiltinLoopAgentRunner {
  public constructor(
    definition: AgentDefinition,
    private readonly mediaStore: MediaStore,
  ) {
    super(definition);
  }

  public buildUserMessageWithImages(text: string, _images: readonly AgentImageReference[]): FrameworkMessage {
    // The session stores the references on the message; the model call reads them there.
    return this.buildUserMessage(text);
  }

  public override async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const images = await loadImages(this.mediaStore, collectTurnImages(turn));
    return loadedImages.run(images, () => super.runModelStep(turn));
  }
}

function createScriptedSessionFlow(
  context: HarnessContext,
  tools: readonly FunctionTool[],
  supportsImages: boolean,
): AgentSessionFlow<BuiltinLoopAgentRunner> {
  const models = new Map<string, ModelClient>();
  const modelFor = (model: string, reasoningEffort: string | undefined): ModelClient => {
    const key = JSON.stringify([model, reasoningEffort ?? null]);
    let client = models.get(key);
    if (client === undefined) {
      const scripted = new ScriptedModelClient((request) => respond(request, model));
      client =
        reasoningEffort === undefined
          ? scripted
          : {
              callModel: (request, output) =>
                scripted.callModel({ ...request, settings: { ...request.settings, reasoningEffort } }, output),
              estimateInputTokens: (request) => scripted.estimateInputTokens(request),
            };
      models.set(key, client);
    }
    return client;
  };
  const definition: AgentDefinition = {
    name: "scripted",
    instructions: (turn) => context.resolveTurn(turn.sessionSettings ?? {}).instructions ?? "You are a scripted test agent.",
    model: modelFor(context.config.model, undefined),
    resolveModel: (settings) => {
      const turn = context.resolveTurn(settings);
      return modelFor(turn.model, turn.reasoningEffort);
    },
    toolSources: context.toolSources,
    tools,
  };
  const summaryAttributesIndexed = context.isSessionSummaryIndexed === true;
  if (!supportsImages || context.mediaStore === undefined) {
    return createBuiltinAgentSessionFlow(definition, context.flowType, { summaryAttributesIndexed });
  }
  return createAgentSessionFlow(new ImageScriptedRunner(definition, context.mediaStore), context.flowType, {
    summaryAttributesIndexed,
  });
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
  if (text === "effort") {
    return { text: `effort: ${request.settings.reasoningEffort ?? "default"}` };
  }
  if (text === "instructions") {
    return { text: `instructions: ${request.instructions}` };
  }
  if (text === "history") {
    return { text: `history: ${ScriptedModelClient.userTexts(request).join(" | ")}` };
  }
  if (text === "images") {
    const seen = [...(loadedImages.getStore() ?? new Map<string, Uint8Array>())].map(
      ([sha256, data]) => `saw image ${sha256} ${data.length}`,
    );
    return { text: seen.length === 0 ? "saw no images" : seen.join("; ") };
  }
  const call = /^call (\S+)(?: (.*))?$/s.exec(text);
  if (call !== null) {
    const lastResult = results.at(-1);
    if (lastResult !== undefined) {
      return { text: `result: ${lastResult.output}` };
    }
    return { toolCalls: [{ toolName: call[1] ?? "", arguments: call[2] ?? "{}" }] };
  }
  if (text === "leak") {
    writeSync(1, "host wrote to fd 1\n");
    writeSync(2, "host wrote to fd 2\n");
    await new Promise<void>((resolve) => {
      const child = spawn("sh", ["-c", "echo grandchild wrote to stdout; echo grandchild wrote to stderr >&2"], {
        stdio: ["ignore", "inherit", "inherit"],
      });
      child.once("exit", () => resolve());
      child.once("error", () => resolve());
    });
    return { text: "leaked" };
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
