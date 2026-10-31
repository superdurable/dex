// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

/** History event type of a completed `waitFor` method. */
export const waitForCompletedEvent = "StepWaitForCompleted";
/** History event type of a completed `execute` method. */
export const executeCompletedEvent = "StepExecuteCompleted";

/** One completed Step method in a Flow's history, returned by {@link readStepHistory}. */
export interface StepHistoryEntry {
  /** {@link waitForCompletedEvent} or {@link executeCompletedEvent}. */
  readonly eventType: string;
  /** Step type, such as `"RunModel"`. */
  readonly stepType: string;
  /** Step execution ID, such as `"RunModel-2"`. */
  readonly stepExecutionId: string;
  /** One-based attempt that completed the method. */
  readonly finalAttempt: number;
}

/** Options of {@link readStepHistory}. */
export interface ReadStepHistoryOptions {
  /** Path to `dexcli`. Defaults to the `DEXCLI` environment variable, then `dexcli` on `PATH`. */
  readonly dexcliPath?: string;
}

/**
 * Returns every completed Step method of a Flow's current run, in history order.
 *
 * It runs `dexcli flow history --all --output json`, as the Python core's `read_step_history` does. Use
 * it to assert the Step shape of a turn, such as `RunModel`, `CallTool`, `ResumeTurn`, `RunModel`.
 * Steps that are still waiting or running are not included.
 *
 * @example
 * ```ts
 * const executed = (await readStepHistory(engine.serverAddress, sessionId))
 *   .filter((entry) => entry.eventType === executeCompletedEvent)
 *   .map((entry) => entry.stepType);
 * ```
 * @param serverAddress - Dex Server address.
 * @param flowId - Flow ID to read.
 * @param options - `dexcli` path.
 * @returns One entry per completed `waitFor` or `execute` method.
 * @throws {Error} When `dexcli` fails or prints unexpected JSON.
 */
export async function readStepHistory(
  serverAddress: string,
  flowId: string,
  options: ReadStepHistoryOptions = {},
): Promise<StepHistoryEntry[]> {
  const dexcliPath = options.dexcliPath ?? process.env.DEXCLI ?? "dexcli";
  const { stdout } = await execFileAsync(
    dexcliPath,
    ["flow", "history", flowId, "--server", serverAddress, "--all", "--output", "json"],
    { maxBuffer: 64 * 1024 * 1024 },
  );
  const history = JSON.parse(stdout) as { events?: unknown };
  if (!Array.isArray(history.events)) {
    throw new Error(`dexcli flow history printed no events for ${flowId}`);
  }
  const entries: StepHistoryEntry[] = [];
  for (const event of history.events as Array<{ type?: unknown; payload?: unknown }>) {
    if (event.type !== waitForCompletedEvent && event.type !== executeCompletedEvent) {
      continue;
    }
    const stepContext = (event.payload as { context?: Record<string, unknown> } | undefined)?.context;
    if (stepContext === undefined) {
      throw new Error(`history event ${event.type} of ${flowId} has no Step context`);
    }
    entries.push({
      eventType: event.type,
      stepType: String(stepContext.stepType),
      stepExecutionId: String(stepContext.stepExecutionId),
      finalAttempt: Number(stepContext.finalAttempt),
    });
  }
  return entries;
}
