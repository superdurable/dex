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
  DeferredToolOutcome,
  ModelCallMetadata,
} from "./agent-framework-runner.js";
import type { AgentMessage, AgentUsage, ContextSummary, SessionSettings } from "./agent-session-values.js";

/**
 * When a session summarizes its earlier messages so the model input stays under the context window, passed
 * as {@link AgentSessionOptions.compaction} or as `AgentDefinition.compaction` for a Stepwise agent.
 *
 * Before each model call, `RunModel` asks the runner for the size of the next model input. When it reaches
 * {@link triggerRatio} of {@link contextWindowTokens}, `RunModel` makes no model call and moves to
 * `CompactContext`, which folds the earlier messages into a {@link ContextSummary}, then back to
 * `RunModel`. The runner must implement {@link AgentFrameworkRunner.estimateInputTokens},
 * {@link AgentFrameworkRunner.selectCompactionCutoff}, and {@link AgentFrameworkRunner.summarizeContext};
 * `createAgentSessionFlow` throws otherwise. Messages stay in `AgentMessages`; only the model input
 * changes. Enable compaction only after every Worker of the Flow type runs a release with the
 * `CompactContext` Step.
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
}

/** A {@link CompactionPolicy} with every default filled in. */
export interface ResolvedCompactionPolicy {
  /** The model's context window in tokens. */
  readonly contextWindowTokens: number;
  /** Share of the window at which the session compacts. */
  readonly triggerRatio: number;
  /** Share of the window that the newest messages may keep. */
  readonly retainedRatio: number;
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
 * @throws {RangeError} When `contextWindowTokens` is not a positive integer, or unless
 * `0 < retainedRatio < triggerRatio <= 1`.
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
  return Object.freeze({ contextWindowTokens, triggerRatio, retainedRatio });
}

/** Reports whether a model input of this size makes the session compact before the model call. */
export function isCompactionTriggered(policy: ResolvedCompactionPolicy, estimatedInputTokens: number): boolean {
  return estimatedInputTokens >= policy.triggerRatio * policy.contextWindowTokens;
}

/** Tokens that the newest messages may keep after compaction: the retained share of the window, rounded down. */
export function readRetainedTokenBudget(policy: ResolvedCompactionPolicy): number {
  return Math.floor(policy.retainedRatio * policy.contextWindowTokens);
}
