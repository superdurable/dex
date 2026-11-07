// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { appendFileSync, existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import {
  createAgentSessionFlow,
  type AgentSessionFlow,
  type AgentSessionOptions,
  type ModelSpendLimit,
  type AgentTracingOptions,
  type ContextSummaryRequest,
  type ContextSummaryResult,
  type ModelCallMetadata,
} from "../../src/index.js";
import {
  StepwiseAgentRunner,
  contextSummaryInstructions,
  contextSummaryNoteHeader,
  functionTool,
  type AgentDefinition,
  type ModelInputItem,
  type ModelRequest,
} from "../../src/stepwise/index.js";
import { FileGate, InvocationLog, ScriptedModelClient, type ScriptedModelReply } from "../../src/testing/index.js";

/** Flow type of the compaction test agent, the same as the Python core's. */
export const compactionTestAgentFlowType = "CompactionScriptedAgent";
/** Environment variable naming the marker directory of a compaction test Worker child. */
export const compactionMarkerDirectoryVariable = "COMPACTION_AGENT_STATE_DIR";
/** Environment variable that sets the context window in tokens. */
export const compactionWindowTokensVariable = "COMPACTION_AGENT_WINDOW_TOKENS";
/** Environment variable that pads every summary with this many characters. */
export const compactionSummaryPadVariable = "COMPACTION_AGENT_SUMMARY_PAD_CHARACTERS";
/** Environment variable that sets `messageWindowSize`. */
export const compactionMessageWindowSizeVariable = "COMPACTION_AGENT_MESSAGE_WINDOW_SIZE";
/** Environment variable that sets `messageChunkSize`. */
export const compactionMessageChunkSizeVariable = "COMPACTION_AGENT_MESSAGE_CHUNK_SIZE";
/** Environment variable that sets `retainedMessageLimit`; `none` keeps every message. */
export const compactionRetainedMessageLimitVariable = "COMPACTION_AGENT_RETAINED_MESSAGE_LIMIT";
/** File in the marker directory whose presence makes every summary call fail. */
export const failSummaryFileName = "fail-summary";
/** FileGate a summary call waits at when a summarized command contains `gate-summary`. */
export const summaryGateName = "summary";
const defaultWindowTokens = 600;
const commandsPrefix = "commands: ";
const toolResultsPrefix = "tool results: ";
const summaryCommandLimit = 10;
const lookupDetail = " detail".repeat(30);

/** One logged model call; `items` describe the input as `[kind, ...fields]`. */
export interface LoggedCompactionModelCall {
  readonly kind: "model" | "summary";
  readonly turnId: string;
  readonly attempt: number;
  readonly items: readonly (readonly unknown[])[];
}

/**
 * Creates the compaction test agent, the TypeScript twin of the Python core's `tests/compaction_agent.py`.
 *
 * The model reads the turn's command: the last user text, or, once compaction summarized it, the last
 * command the summary lists. `pad <characters> <tag>` answers `<tag>:` and that many `x`. `chain <count>`
 * calls `lookup_order` once per model call until it has `<count>` results, then answers
 * `chain done: <count> lookups`. A summary call answers `commands: <c1> || <c2> ...` with the newest ten
 * commands and `tool results: <n>`, folding in the previous summary, then the configured padding. It
 * waits at the `summary` FileGate when a summarized command contains `gate-summary`, and throws while
 * the `fail-summary` file exists. `plan <task>` calls `write_todos` with one task in progress, then
 * answers `planned`. Like a provider, the model refuses a request whose input exceeds the context window.
 * Every model call is appended to `model-requests.jsonl`.
 */
export function createCompactionTestAgentFlow(
  markerDirectory: string,
  windowTokens = defaultWindowTokens,
  summaryPadCharacters = 0,
  options: CompactionTestAgentOptions = {},
): AgentSessionFlow<StepwiseAgentRunner> {
  const invocations = new InvocationLog(join(markerDirectory, "invocations.log"));
  const respond = async (request: ModelRequest): Promise<ScriptedModelReply> => {
    const isSummary = request.instructions === contextSummaryInstructions;
    logModelCall(markerDirectory, request, isSummary ? "summary" : "model");
    if (model.estimateInputTokens(request) > windowTokens) {
      throw new Error("the request exceeds the context window");
    }
    if (isSummary) {
      return summarize(markerDirectory, request, summaryPadCharacters);
    }
    const { commands, results, isSummaryLast } = readConversation(request.inputItems);
    const command = commands.at(-1) ?? "";
    const [verb = "", ...words] = command.split(" ");
    if (verb === "pad") {
      const [characters = "0", ...tag] = words;
      return { text: `${tag.join(" ")}:${"x".repeat(Number(characters))}` };
    }
    if (verb === "chain") {
      // The summary is the first item, so it and the shown results count every lookup.
      const done = isSummaryLast ? results : ScriptedModelClient.toolResults(request).length;
      if (done >= Number(words[0])) {
        return { text: `chain done: ${done} lookups` };
      }
      return { toolCalls: [{ toolName: "lookup_order", arguments: { step: done } }] };
    }
    if (verb === "plan") {
      if (ScriptedModelClient.toolResults(request).length > 0) {
        return { text: "planned" };
      }
      return {
        toolCalls: [{ toolName: "write_todos", arguments: { todos: [{ content: words.join(" "), status: "in_progress" }] } }],
      };
    }
    throw new Error(`unknown scripted command ${JSON.stringify(command)}`);
  };
  const model = new ScriptedModelClient(respond);
  const definition: AgentDefinition = {
    name: "compaction-test",
    instructions: "You are a scripted compaction test agent.",
    model,
    tools: [
      functionTool<{ step: number }>({
        name: "lookup_order",
        description: "Look up one order.",
        parameters: {
          type: "object",
          properties: { step: { type: "integer" } },
          required: ["step"],
          additionalProperties: false,
        },
        effect: "read",
        execute: ({ step }) => {
          invocations.record(`lookup_order:${step}`);
          return `order ${step}:${lookupDetail}`;
        },
      }),
    ],
    builtInTools: ["write_todos"],
    compaction: { contextWindowTokens: windowTokens },
  };
  const runner =
    options.summaryModelCallMetadata === undefined
      ? new StepwiseAgentRunner(definition)
      : new SummaryMetadataRunner(definition, options.summaryModelCallMetadata);
  return createAgentSessionFlow(runner, options.flowType ?? compactionTestAgentFlowType, {
    builtInTools: ["write_todos"],
    compaction: { contextWindowTokens: windowTokens },
    ...(options.tracing === undefined ? {} : { tracing: options.tracing }),
    ...(options.spendLimit === undefined ? {} : { spendLimit: options.spendLimit }),
    ...options.messageStorage,
  });
}

/** Variants of the compaction test agent. */
export interface CompactionTestAgentOptions {
  readonly flowType?: string;
  readonly tracing?: AgentTracingOptions;
  /** The session's spend limit. */
  readonly spendLimit?: ModelSpendLimit;
  /** Metadata every summary result reports, for the `CompactContext` span. */
  readonly summaryModelCallMetadata?: ModelCallMetadata;
  /** Window, chunk, and retention options of the stored messages. */
  readonly messageStorage?: Pick<
    AgentSessionOptions,
    "messageWindowSize" | "messageChunkSize" | "retainedMessageLimit"
  >;
}

/** Reads the message storage options a Worker child gets from its environment. */
export function readMessageStorageEnvironment(
  environment: NodeJS.ProcessEnv,
): NonNullable<CompactionTestAgentOptions["messageStorage"]> {
  const windowSize = environment[compactionMessageWindowSizeVariable];
  const chunkSize = environment[compactionMessageChunkSizeVariable];
  const retainedLimit = environment[compactionRetainedMessageLimitVariable];
  return {
    ...(windowSize === undefined ? {} : { messageWindowSize: Number(windowSize) }),
    ...(chunkSize === undefined ? {} : { messageChunkSize: Number(chunkSize) }),
    ...(retainedLimit === undefined
      ? {}
      : { retainedMessageLimit: retainedLimit === "none" ? null : Number(retainedLimit) }),
  };
}

class SummaryMetadataRunner extends StepwiseAgentRunner {
  public constructor(
    definition: AgentDefinition,
    private readonly metadata: ModelCallMetadata,
  ) {
    super(definition);
  }

  public override async summarizeContext(request: ContextSummaryRequest): Promise<ContextSummaryResult> {
    return { ...(await super.summarizeContext(request)), modelCallMetadata: this.metadata };
  }
}

/**
 * Reads every logged model call, oldest first.
 * @param markerDirectory - The agent's marker directory.
 * @returns The calls.
 */
export function readLoggedCompactionModelCalls(markerDirectory: string): LoggedCompactionModelCall[] {
  const path = join(markerDirectory, "model-requests.jsonl");
  if (!existsSync(path)) {
    return [];
  }
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((line) => line !== "")
    .map((line) => JSON.parse(line) as LoggedCompactionModelCall);
}

async function summarize(
  markerDirectory: string,
  request: ModelRequest,
  padCharacters: number,
): Promise<ScriptedModelReply> {
  // The last item is the request to summarize, not part of the conversation.
  const { commands, results } = readConversation(request.inputItems.slice(0, -1));
  if (commands.some((command) => command.includes("gate-summary"))) {
    const gate = new FileGate(markerDirectory, summaryGateName);
    gate.markReached();
    await gate.waitForRelease(request.cancellationSignal);
  }
  if (existsSync(join(markerDirectory, failSummaryFileName))) {
    throw new Error("scripted summary failure");
  }
  const text = `${commandsPrefix}${commands.slice(-summaryCommandLimit).join(" || ")}\n${toolResultsPrefix}${results}`;
  return { text: padCharacters > 0 ? `${text}\npadding: ${"y".repeat(padCharacters)}` : text };
}

/** Returns the user commands in order, the tool results so far, and whether a summary is the last user text. */
function readConversation(items: readonly ModelInputItem[]): {
  commands: string[];
  results: number;
  isSummaryLast: boolean;
} {
  const commands: string[] = [];
  let results = 0;
  let isSummaryLast = false;
  for (const item of items) {
    if (item.type === "tool_result") {
      results += 1;
    } else if (item.type === "user_text" && item.text.startsWith(contextSummaryNoteHeader)) {
      const [, commandLine = "", resultLine = ""] = item.text.split("\n");
      commands.push(...commandLine.slice(commandsPrefix.length).split(" || "));
      results += Number(resultLine.slice(toolResultsPrefix.length));
      isSummaryLast = true;
    } else if (item.type === "user_text" && !item.text.startsWith("[Dex]")) {
      commands.push(item.text);
      isSummaryLast = false;
    }
  }
  return { commands, results, isSummaryLast };
}

function logModelCall(markerDirectory: string, request: ModelRequest, kind: LoggedCompactionModelCall["kind"]): void {
  const call: LoggedCompactionModelCall = {
    kind,
    turnId: request.metadata.turnId,
    attempt: request.metadata.attempt,
    items: request.inputItems.map((item) => {
      switch (item.type) {
        case "user_text":
          return ["user", item.text];
        case "tool_result":
          return ["result", item.toolCallId, item.output];
        case "model_output":
          return ["output", item.text, item.toolCalls.map((toolCall) => toolCall.toolCallId)];
      }
    }),
  };
  appendFileSync(join(markerDirectory, "model-requests.jsonl"), `${JSON.stringify(call)}\n`);
}
