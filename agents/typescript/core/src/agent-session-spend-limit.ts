// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentUsage, SessionSettings } from "./agent-session-values.js";

/**
 * How long the session waits for {@link ModelSpendLimit.checkModelCall} or {@link ModelSpendLimit.reportModelUsage},
 * in milliseconds. Bound your own calls below it. A check that takes longer counts as a check that threw. A report
 * that takes longer is dropped.
 *
 * @since 0.3.0
 */
export const spendLimitCallTimeoutMs = 30_000;

/**
 * Starts `AgentState.lastTurnError` of a turn that a spend limit ended. The text goes on with `": "` and the reason
 * from {@link ModelCallDecision}. Test for it to tell a refused call from a failed one.
 *
 * @since 0.3.0
 */
export const modelCallRefusedErrorPrefix = "the model call was refused";

/**
 * Describes one model call that is about to run. The session builds one for every attempt of `RunModel` and
 * `CompactContext`, because every attempt can cost money.
 *
 * @since 0.3.0
 */
export interface ModelCallCheck {
  /** The session's Flow ID. */
  readonly flowId: string;
  /** The turn that makes the call. */
  readonly turnId: string;
  /** `"RunModel"` or `"CompactContext"`. */
  readonly stepType: string;
  /** One-based attempt of the Step. */
  readonly attempt: number;
  /** The turn's session settings. Put the account or API key reference that the limit applies to here. Never a secret. */
  readonly sessionSettings: SessionSettings;
  /** Committed usage of this session so far. */
  readonly sessionUsage: AgentUsage;
}

/**
 * Says whether a model call may run. `reason` reaches `AgentState.lastTurnError`, so people read it, and it is stored.
 * Never put a secret in it.
 *
 * @since 0.3.0
 */
export type ModelCallDecision = { readonly isAllowed: true } | { readonly isAllowed: false; readonly reason: string };

/**
 * Reports what one finished model call used.
 *
 * @since 0.3.0
 */
export interface ModelUsageReport {
  /**
   * Stable ID of this attempt's call: `<flow>/<turn>/<step execution>/<attempt>`. Count each ID once. A retry has a new
   * ID because it is a new paid call.
   */
  readonly reportId: string;
  /** The session's Flow ID. */
  readonly flowId: string;
  /** The turn that made the call. */
  readonly turnId: string;
  /** `"RunModel"` or `"CompactContext"`. */
  readonly stepType: string;
  /** One-based attempt of the Step. */
  readonly attempt: number;
  /** The turn's session settings. */
  readonly sessionSettings: SessionSettings;
  /**
   * What the runner reported. A harness runner, such as the Claude Agent SDK adapter, reports a whole segment here:
   * several model calls in `usage.modelCalls`. Zero tokens with `modelCalls` above zero mean the framework did not
   * expose token counts, not that the call was free.
   */
  readonly usage: AgentUsage;
  /**
   * `usage` added to the committed session usage. A later report covers a report that was lost, for an application
   * that counts per session.
   */
  readonly sessionUsage: AgentUsage;
}

/**
 * Lets an application decide whether a session may spend on a model call.
 *
 * A spend limit is usually per account or per API key, across many sessions, so the counters and the limit belong to
 * the application. Pass an implementation as `AgentSessionOptions.spendLimit`. Without one, the session makes no
 * checks. The object lives in the Worker and is not stored.
 *
 * The session calls {@link checkModelCall} before each attempt of `RunModel` and `CompactContext`, and
 * {@link reportModelUsage} after the attempt's model call returns. Both run inside the Step, on the Worker, so keep
 * them fast and bound them below {@link spendLimitCallTimeoutMs}.
 *
 * A refused call ends the turn: the session records {@link modelCallRefusedErrorPrefix} and the reason in
 * `AgentState.lastTurnError`, keeps the turn's finished tool results, and waits for the next user message, which is
 * checked again. A check that throws or times out fails closed. It fails the attempt, Dex retries it under the Step's
 * retry policy, and no model call runs without a passed check. When the retries run out the turn fails. To fail open,
 * catch your own errors in `checkModelCall` and return `{ isAllowed: true }`.
 *
 * A report that throws or times out is logged and dropped, because failing the attempt would repeat a call that was
 * already paid for.
 *
 * Limits to design for:
 *
 * - A check bounds the next call, not its cost. Sessions that pass together can overshoot a limit. A harness runner is
 *   checked once per segment, so the overshoot can be a whole segment. Set the harness's own budget option too, such
 *   as `maxBudgetUsd` for the Claude Agent SDK.
 * - A call that fails, is cancelled, or is lost with its Worker reports nothing, though the provider may bill it. So
 *   the reports can be lower than the bill. They can also exceed `AgentUsage`: a call whose Step did not commit reports
 *   here and again on its retry, under a new `reportId`, while `AgentUsage` keeps only the committed attempt.
 *
 * @example
 * ```ts
 * const spendLimit: ModelSpendLimit = {
 *   async checkModelCall(check) {
 *     const account = check.sessionSettings.account as string;
 *     return (await spentThisMonth(account)) >= limitOf(account)
 *       ? { isAllowed: false, reason: "account over its monthly limit" }
 *       : { isAllowed: true };
 *   },
 *   async reportModelUsage(report) {
 *     await recordOnce(report.reportId, report.usage);
 *   },
 * };
 * const flow = createAgentSessionFlow(runner, "SupportAgent", { spendLimit });
 * ```
 *
 * @since 0.3.0
 */
export interface ModelSpendLimit {
  /**
   * Decides whether one model call may run.
   * @param check - The call that is about to run.
   * @returns The decision.
   * @throws Any error fails the attempt, so the call does not run.
   */
  checkModelCall(check: ModelCallCheck): Promise<ModelCallDecision>;
  /**
   * Records what one finished model call used.
   * @param report - The call's usage. Count each `reportId` once.
   * @returns A Promise that settles when the usage is recorded.
   * @throws Any error is logged and dropped.
   */
  reportModelUsage(report: ModelUsageReport): Promise<void>;
}

/**
 * Settles with `operation`, or rejects after {@link spendLimitCallTimeoutMs}.
 * @param description - What is running, for the timeout message.
 * @param operation - The application's call.
 * @returns The call's result.
 * @throws {Error} When the call takes too long, or what the call threw.
 */
export async function withinSpendLimitCallTimeout<Result>(
  description: string,
  operation: Promise<Result>,
): Promise<Result> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(
      () => reject(new Error(`${description} took more than ${spendLimitCallTimeoutMs} ms`)),
      spendLimitCallTimeoutMs,
    );
  });
  try {
    return await Promise.race([operation, timeout]);
  } finally {
    clearTimeout(timer);
  }
}
