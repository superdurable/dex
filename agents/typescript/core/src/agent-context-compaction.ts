// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  AgentEventWriter,
  AgentFrameworkRunner,
  AgentTurnContext,
  DeferredToolOutcome,
  ModelCallMetadata,
} from "./agent-framework-runner.js";
import { buildSenderNamedText } from "./agent-sender-names.js";
import type { AgentMessage, AgentUsage, ContextSummary, SessionSettings } from "./agent-session-values.js";

/**
 * When a session summarizes its earlier messages so the model input stays under the context window, passed
 * as {@link AgentSessionOptions.compaction} or as `AgentDefinition.compaction` for a built-in loop agent.
 *
 * Before each model call, `RunModel` asks the runner for the size of the next model input. When it reaches
 * {@link triggerRatio} of {@link contextWindowTokens}, `RunModel` makes no model call and moves to
 * `CompactContext`, which folds the earlier messages into a {@link ContextSummary}, then back to
 * `RunModel`. The runner must implement {@link AgentFrameworkRunner.estimateInputTokens},
 * {@link AgentFrameworkRunner.selectCompactionCutoff}, and {@link AgentFrameworkRunner.summarizeContext};
 * `createAgentSessionFlow` throws otherwise. Summarized messages stay stored until
 * `AgentSessionOptions.retainedMessageLimit` deletes them; only the model input changes. Enable
 * compaction only after every Worker of the Flow type runs a release with the `CompactContext` Step.
 *
 * @example
 * ```ts
 * const flow = createAgentSessionFlow(runner, "SupportAgent", {
 *   compaction: { contextWindowTokens: 400_000 },
 * });
 * ```
 */
export interface CompactionPolicy {
  /** The model's context window, in the tokens that the runner's `estimateInputTokens` counts. */
  readonly contextWindowTokens: number;
  /** Share of the window at which the session compacts before the next model call. Defaults to 0.85. */
  readonly triggerRatio?: number;
  /** Share of the window that the newest messages may keep unsummarized. Defaults to 0.3. */
  readonly retainedRatio?: number;
  /**
   * Tokens of every model input that the runner's estimate cannot see, such as a system prompt that a
   * framework builds, a dynamic instruction, or tools listed at run time. The session adds them to the
   * estimate. Defaults to 0.
   * @since 0.3.0
   */
  readonly reservedInputTokens?: number;
}

/** A {@link CompactionPolicy} with every default filled in. */
export interface ResolvedCompactionPolicy {
  /** The model's context window in tokens. */
  readonly contextWindowTokens: number;
  /** Share of the window at which the session compacts. */
  readonly triggerRatio: number;
  /** Share of the window that the newest messages may keep. */
  readonly retainedRatio: number;
  /**
   * Tokens added to every estimate. `undefined` reads as 0.
   * @since 0.3.0
   */
  readonly reservedInputTokens?: number;
}

/** First line of the user text that carries the {@link ContextSummary} into the model input. */
export const contextSummaryNoteHeader = "[Dex] Summary of the earlier conversation:";

/** Instructions of the model call that writes a {@link ContextSummary}. */
export const contextSummaryInstructions =
  "You summarize the earlier part of an agent conversation. The agent continues from your " +
  "summary and the messages after it, so keep every fact, decision, user request and " +
  "preference, open task, and tool result that it still needs, including what an earlier " +
  "summary says. Write plain text.";

/** Last user text of the model call that writes a {@link ContextSummary}. */
export const contextSummaryRequest = "[Dex] Summarize the conversation above. Reply with the summary only.";

/**
 * First line of the transcript in the prompt of {@link buildContextSummaryPrompt}.
 * @since 0.3.0
 */
export const contextSummaryTranscriptHeader = "[Dex] The conversation to summarize:";

/**
 * The most tokens {@link buildContextSummaryPrompt} leaves free for the summary it asks for.
 * @since 0.3.0
 */
export const contextSummaryOutputTokens = 4096;

// Bytes per estimated token. Below the 4 of English text, and 1 token per CJK character.
const estimatedBytesPerToken = 3;
const textEncoder = new TextEncoder();

/**
 * Returns the user text that says how many of the oldest messages a summary request left out.
 * @param omittedCount - Messages left out, at least 1.
 * @returns `[Dex] <omittedCount> earlier messages did not fit in this request and are left out.`, or
 * `[Dex] 1 earlier message did not fit in this request and is left out.` for one message.
 */
export function buildContextSummaryOmissionNote(omittedCount: number): string {
  if (omittedCount === 1) {
    return "[Dex] 1 earlier message did not fit in this request and is left out.";
  }
  return `[Dex] ${omittedCount} earlier messages did not fit in this request and are left out.`;
}

/**
 * Returns the user text that puts a summary before the newer messages in a model input.
 * @param summary - The session's summary.
 * @returns {@link contextSummaryNoteHeader}, a newline, and the summary text.
 */
export function buildContextSummaryNote(summary: ContextSummary): string {
  return `${contextSummaryNoteHeader}\n${summary.text}`;
}

/**
 * Estimates the tokens of a text without a tokenizer, rounding up.
 *
 * Use it where a framework assembles the prompt and offers no token counter. It counts one token per three UTF-8
 * bytes. That is more than a provider counts for English text, and about one token per CJK character, so the
 * estimate errs toward compacting early.
 *
 * @example
 * ```ts
 * estimateTextTokens("twelve bytes"); // 4
 * ```
 * @param text - The text to measure.
 * @returns The estimated tokens; 0 for an empty text.
 * @since 0.3.0
 */
export function estimateTextTokens(text: string): number {
  return Math.ceil(textEncoder.encode(text).length / estimatedBytesPerToken);
}

/**
 * Estimates a model input that a framework builds from the stored items.
 *
 * Call it from `estimateInputTokens` of a framework adapter. It measures each `frameworkItem` as stored, so JSON
 * syntax and opaque provider items count too, plus the summary and any `frameworkRunState`.
 *
 * @example
 * ```ts
 * public estimateInputTokens(turn: AgentTurnContext): number {
 *   return estimateStoredInputTokens(turn, estimateTextTokens(this.instructions));
 * }
 * ```
 * @param turn - The context of the next model call.
 * @param fixedInputTokens - Tokens of what every request repeats, such as instructions and tool definitions.
 * Measure what the framework shows; `CompactionPolicy.reservedInputTokens` covers the rest. Defaults to 0.
 * @returns The estimated input tokens.
 * @since 0.3.0
 */
export function estimateStoredInputTokens(turn: AgentTurnContext, fixedInputTokens = 0): number {
  let tokens = fixedInputTokens;
  for (const message of turn.messages) {
    tokens += estimateTextTokens(message.frameworkItem);
  }
  if (turn.contextSummary) {
    tokens += estimateTextTokens(buildContextSummaryNote(turn.contextSummary));
  }
  if (turn.frameworkRunState !== null) {
    tokens += estimateTextTokens(turn.frameworkRunState);
  }
  return tokens;
}

/**
 * Chooses the last message to summarize, cutting only between turns.
 *
 * Call it from `selectCompactionCutoff` of a framework adapter. A turn's messages are stored together, so a cut
 * between turns never separates a tool call from its result and never splits a model response, whatever
 * framework items the turn holds. The running turn is always kept, because the adapter reads its opening user
 * message from `turn.messages`.
 *
 * It returns `null` while `turn.frameworkRunState` is set. A resumed model call continues from that value, which
 * may already hold the older messages, so the summary could not replace them. Compaction waits for the next turn.
 *
 * @example
 * ```ts
 * public selectCompactionCutoff(turn: AgentTurnContext, retainedTokenBudget: number): number | null {
 *   return selectTurnBoundaryCutoff(turn, retainedTokenBudget);
 * }
 * ```
 * @param turn - The context of the model call that crossed the threshold.
 * @param retainedTokenBudget - Tokens that the kept messages may use.
 * @returns The sequence of the last message to summarize: the earliest turn boundary whose kept messages fit the
 * budget, else the boundary before the running turn. `null` when the turn is resuming, every message already
 * fits, or no earlier turn is left to summarize.
 * @since 0.3.0
 */
export function selectTurnBoundaryCutoff(turn: AgentTurnContext, retainedTokenBudget: number): number | null {
  if (turn.frameworkRunState !== null) {
    return null;
  }
  const messages = turn.messages;
  const foundRunningTurnIndex = messages.findIndex((message) => message.turnId === turn.turnId);
  const runningTurnIndex = foundRunningTurnIndex === -1 ? messages.length : foundRunningTurnIndex;
  const cuts: number[] = [];
  for (let index = 1; index < Math.min(runningTurnIndex + 1, messages.length); index += 1) {
    if (messages[index]?.turnId !== messages[index - 1]?.turnId) {
      cuts.push(index);
    }
  }
  if (cuts.length === 0) {
    return null;
  }
  const keptTokens = new Array<number>(messages.length + 1).fill(0);
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    keptTokens[index] = (keptTokens[index + 1] ?? 0) + estimateTextTokens(messages[index]?.frameworkItem ?? "");
  }
  if ((keptTokens[0] ?? 0) <= retainedTokenBudget) {
    return null;
  }
  const cut = cuts.find((candidate) => (keptTokens[candidate] ?? 0) <= retainedTokenBudget) ?? cuts.at(-1);
  return cut === undefined ? null : (messages[cut - 1]?.sequence ?? null);
}

/**
 * Returns a stored message as the plain text a summary prompt shows.
 *
 * Wrap it in the `describeMessage` of {@link buildContextSummaryPrompt} to leave out items that only a provider can
 * read.
 *
 * @param message - The stored message.
 * @returns Its role and text, tool arguments, and tool output. A message with none of them returns its role and
 * `frameworkItem`, such as a model response with several tool calls. Changed in 0.6.3: the text of a message with a
 * `senderName` starts with `"<senderName>: "`, as the model saw it.
 * @since 0.3.0
 */
export function describeStoredMessage(message: AgentMessage): string {
  const lines: string[] = [];
  if (message.text) {
    lines.push(`${message.role}: ${buildSenderNamedText(message.senderName, message.text)}`);
  }
  if (message.toolArguments !== null) {
    lines.push(`tool call ${message.toolName ?? ""}: ${message.toolArguments}`);
  }
  if (message.toolOutput !== null) {
    lines.push(`tool result ${message.toolName ?? ""}: ${message.toolOutput}`);
  }
  return lines.length > 0 ? lines.join("\n") : `${message.role}: ${message.frameworkItem}`;
}

/**
 * Builds the one user prompt of a summary call for a framework adapter.
 *
 * Call it from `summarizeContext` and send the result with {@link contextSummaryInstructions} and no tools. The
 * prompt holds the previous summary, the messages as plain text, and {@link contextSummaryRequest}. Plain text
 * works with any framework: it needs no tool definitions, and no provider sees an item out of its place.
 *
 * The prompt fits `request.inputTokenBudget`, less room for the summary, as {@link estimateTextTokens} counts.
 * When the range is too large, it leaves out the oldest messages and says so with
 * {@link buildContextSummaryOmissionNote}. It shortens a single message only when that message alone is too
 * large.
 *
 * @example
 * ```ts
 * public async summarizeContext(request: ContextSummaryRequest): Promise<ContextSummaryResult> {
 *   const result = await generateText({
 *     model,
 *     system: contextSummaryInstructions,
 *     prompt: buildContextSummaryPrompt(request),
 *   });
 *   return { text: result.text, usage: toAgentUsage(result.usage) };
 * }
 * ```
 * @param request - The previous summary and the messages to summarize.
 * @param describeMessage - Returns a message's text for the prompt, or `null` to leave it out, such as an
 * encrypted reasoning item. Defaults to {@link describeStoredMessage}.
 * @returns The prompt text.
 * @since 0.3.0
 */
export function buildContextSummaryPrompt(
  request: ContextSummaryRequest,
  describeMessage: (message: AgentMessage) => string | null = describeStoredMessage,
): string {
  const opening = request.previousSummary === null ? [] : [buildContextSummaryNote(request.previousSummary)];
  const closing = [contextSummaryTranscriptHeader, contextSummaryRequest];
  const descriptions = request.messages.flatMap((message) => {
    const description = describeMessage(message);
    return description === null ? [] : [description];
  });
  let remainingTokens: number | undefined;
  if (request.inputTokenBudget !== undefined) {
    const fixedTokens = estimateTextTokens([contextSummaryInstructions, ...opening, ...closing].join("\n\n"));
    const roomForTranscript = request.inputTokenBudget - fixedTokens;
    remainingTokens =
      roomForTranscript - Math.min(contextSummaryOutputTokens, Math.floor(Math.max(roomForTranscript, 0) / 4));
  }
  const kept: string[] = [];
  for (const description of [...descriptions].reverse()) {
    if (remainingTokens === undefined) {
      kept.push(description);
      continue;
    }
    // Two newlines separate messages; they count toward the estimate.
    const tokens = estimateTextTokens(`${description}\n\n`);
    if (tokens <= remainingTokens) {
      kept.push(description);
      remainingTokens -= tokens;
      continue;
    }
    if (kept.length === 0 && remainingTokens > 0) {
      kept.push(shortenToTokens(description, remainingTokens));
    }
    break;
  }
  kept.reverse();
  const omittedCount = descriptions.length - kept.length;
  if (omittedCount > 0) {
    opening.push(buildContextSummaryOmissionNote(omittedCount));
  }
  return [...opening, contextSummaryTranscriptHeader, ...kept, contextSummaryRequest].join("\n\n");
}

function shortenToTokens(text: string, tokens: number): string {
  const marker = "[Dex] The rest of this message is left out.";
  const keptBytes = Math.max(tokens - estimateTextTokens(marker) - 1, 0) * estimatedBytesPerToken;
  const kept = new TextDecoder().decode(textEncoder.encode(text).slice(0, keptBytes)).replace(/\uFFFD$/u, "");
  return `${kept}\n${marker}`;
}

/** What {@link AgentFrameworkRunner.summarizeContext} receives: the messages to fold into a new summary. */
export interface ContextSummaryRequest {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn ID of the running turn. */
  readonly turnId: string;
  /** One-based `CompactContext` attempt. */
  readonly attempt: number;
  /** Dex Step execution ID of the `CompactContext` Step, shared by all of its attempts. */
  readonly stepExecutionId: string;
  /** The summary so far, or `null` for the first one. Fold its text into the new summary. */
  readonly previousSummary: ContextSummary | null;
  /**
   * The messages after the previous summary through {@link throughSequence}, in sequence order. No tool
   * call is separated from its result.
   */
  readonly messages: readonly AgentMessage[];
  /** Sequence of the last message to summarize. */
  readonly throughSequence: number;
  /** Events writer bound to the turn and attempt. Write no model text to it: the summary is not an answer. */
  readonly events: AgentEventWriter;
  /** Signal aborted when Dex cancels the attempt; pass it to the model call. */
  readonly cancellationSignal: AbortSignal;
  /** Absolute deadline of the current attempt, or `undefined` when Dex supplied none. */
  readonly deadline: Date | undefined;
  /**
   * Tokens the summary call may use, the policy's `contextWindowTokens`, or `undefined` for no limit. A runner
   * keeps the input below it, less what it reserves for the summary, such as its `maxOutputTokens`. The range
   * can be larger after the window shrank or when compaction starts on a long session;
   * leave out its oldest part rather than send an over-window request that fails every turn.
   */
  readonly inputTokenBudget?: number;
  /**
   * The {@link SessionSettings} the running turn started with, as `AgentTurnContext.sessionSettings` has them.
   * Use them to choose the model that writes the summary. The session always sets it; treat `undefined` as `{}`.
   */
  readonly sessionSettings?: SessionSettings;
  /**
   * Records a heartbeat. The session already records one every 20 seconds while the summary runs.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  heartbeat(): Promise<void>;
}

/** What {@link AgentFrameworkRunner.summarizeContext} returns. */
export interface ContextSummaryResult {
  /** Summary text; it must not be blank. It replaces the previous summary, so it includes what that one said. */
  readonly text: string;
  /** Usage of the summary model call; the session adds it to the session's usage. */
  readonly usage: AgentUsage;
  /**
   * Model and response of the summary call, for the `CompactContext` attempt's `chat` span. When omitted,
   * the span is named `chat`.
   */
  readonly modelCallMetadata?: ModelCallMetadata;
}

/** A runner that implements the three optional compaction methods. */
export type ContextCompactionRunner = AgentFrameworkRunner<DeferredToolOutcome> &
  Required<
    Pick<AgentFrameworkRunner<DeferredToolOutcome>, "estimateInputTokens" | "selectCompactionCutoff" | "summarizeContext">
  >;

/** Reports whether a runner implements every compaction method. */
export function isContextCompactionRunner(
  runner: AgentFrameworkRunner<DeferredToolOutcome>,
): runner is ContextCompactionRunner {
  return (
    typeof runner.estimateInputTokens === "function" &&
    typeof runner.selectCompactionCutoff === "function" &&
    typeof runner.summarizeContext === "function"
  );
}

/**
 * Fills in a compaction policy's defaults and checks it.
 * @throws {RangeError} When `contextWindowTokens` is not a positive integer, `reservedInputTokens` is not an
 * integer from 0 to below the window, or unless `0 < retainedRatio < triggerRatio <= 1`.
 */
export function resolveCompactionPolicy(policy: CompactionPolicy): ResolvedCompactionPolicy {
  const contextWindowTokens = policy.contextWindowTokens;
  if (!Number.isSafeInteger(contextWindowTokens) || contextWindowTokens <= 0) {
    throw new RangeError("compaction.contextWindowTokens must be a positive integer");
  }
  const triggerRatio = policy.triggerRatio ?? 0.85;
  const retainedRatio = policy.retainedRatio ?? 0.3;
  if (!(retainedRatio > 0 && retainedRatio < triggerRatio && triggerRatio <= 1)) {
    throw new RangeError("compaction needs 0 < retainedRatio < triggerRatio <= 1");
  }
  const reservedInputTokens = policy.reservedInputTokens ?? 0;
  const isReservationValid =
    Number.isSafeInteger(reservedInputTokens) && reservedInputTokens >= 0 && reservedInputTokens < contextWindowTokens;
  if (!isReservationValid) {
    throw new RangeError("compaction.reservedInputTokens must be an integer from 0 to below contextWindowTokens");
  }
  return Object.freeze({ contextWindowTokens, triggerRatio, retainedRatio, reservedInputTokens });
}

/**
 * Reports whether a model input of this size makes the session compact before the model call: the estimate
 * plus `reservedInputTokens` reaches `triggerRatio` of the window.
 */
export function isCompactionTriggered(policy: ResolvedCompactionPolicy, estimatedInputTokens: number): boolean {
  const estimatedTokens = estimatedInputTokens + (policy.reservedInputTokens ?? 0);
  return estimatedTokens >= policy.triggerRatio * policy.contextWindowTokens;
}

/** Tokens that the newest messages may keep after compaction: the retained share of the window, rounded down. */
export function readRetainedTokenBudget(policy: ResolvedCompactionPolicy): number {
  return Math.floor(policy.retainedRatio * policy.contextWindowTokens);
}
