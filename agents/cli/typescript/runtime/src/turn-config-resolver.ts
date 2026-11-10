// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { AsyncLocalStorage } from "node:async_hooks";

import type {
  AgentFrameworkRunner,
  AgentTurnContext,
  AgentTurnOutcome,
  DeferredToolOutcome,
  SessionSettings,
} from "@superdurable/dex-agents";

import type { ResolvedTurn } from "./agent-harness.js";
import { resolveTurn, type EffectiveConfig } from "./cli-config.js";

const maximumCachedTurns = 64;

/**
 * Resolves each turn's configuration from its session settings and makes it visible to the tool sources.
 *
 * `ToolSource.listTools` receives no turn, so {@link installOn} wraps the runner's `runModelStep` and keeps the
 * running turn's configuration in an `AsyncLocalStorage`. A listing outside a model call sees the file's
 * configuration; the sources still refuse, when the call is built, a tool that its turn switched off.
 *
 * @since 0.5.0
 */
export class TurnConfigResolver {
  private readonly runningTurn = new AsyncLocalStorage<ResolvedTurn>();
  private readonly cachedTurns = new Map<string, ResolvedTurn>();
  private readonly fileTurn: ResolvedTurn;

  /**
   * Creates the resolver.
   * @param effective - The file's effective configuration.
   * @param fileInstructions - The file's instructions, or `null`.
   * @param reportWarnings - Called once per distinct settings with the JSON Pointers of their ignored keys.
   */
  public constructor(
    private readonly effective: EffectiveConfig,
    private readonly fileInstructions: string | null,
    private readonly reportWarnings: (warnings: readonly string[]) => void,
  ) {
    this.fileTurn = { ...effective, instructions: fileInstructions };
  }

  /**
   * Returns a turn's configuration for its session settings.
   * @param sessionSettings - The turn's settings; `{}` for none.
   * @returns The configuration and instructions.
   * @throws {ConfigError} When the settings fail resolution.
   */
  public resolveTurn(sessionSettings: SessionSettings): ResolvedTurn {
    const key = JSON.stringify(sessionSettings);
    const cached = this.cachedTurns.get(key);
    if (cached !== undefined) {
      return cached;
    }
    const { turn, warnings } = resolveTurn(this.effective, sessionSettings);
    if (warnings.length > 0) {
      this.reportWarnings(warnings);
    }
    const resolved: ResolvedTurn = {
      ...turn,
      instructions: turn.system_prompt ?? (turn.system_prompt_file === null ? null : this.fileInstructions),
    };
    if (this.cachedTurns.size >= maximumCachedTurns) {
      this.cachedTurns.clear();
    }
    this.cachedTurns.set(key, resolved);
    return resolved;
  }

  /**
   * Returns the configuration of the turn whose model call is running, or the file's outside a model call.
   * @returns The configuration.
   */
  public currentTurn(): ResolvedTurn {
    return this.runningTurn.getStore() ?? this.fileTurn;
  }

  /**
   * Returns the configuration of a turn from its context, as a tool source's `deferredToolCall` sees it.
   * @param turn - The `RunModel` context.
   * @returns The configuration.
   * @throws {ConfigError} When the settings fail resolution.
   */
  public turnOf(turn: AgentTurnContext): ResolvedTurn {
    return this.resolveTurn(turn.sessionSettings ?? {});
  }

  /**
   * Wraps a runner's `runModelStep` so that each model call first resolves its turn's settings, failing the call
   * when they are rejected, and runs with that configuration in scope.
   *
   * The session Flow calls `runModelStep` through the runner object, so wrapping the Flow's own runner after the
   * harness built the Flow covers every model call.
   * @param runner - The session Flow's runner.
   */
  public installOn(runner: AgentFrameworkRunner<DeferredToolOutcome>): void {
    const runModelStep = runner.runModelStep.bind(runner);
    runner.runModelStep = (turn: AgentTurnContext): Promise<AgentTurnOutcome> => {
      const resolved = this.turnOf(turn);
      return this.runningTurn.run(resolved, () => runModelStep(turn));
    };
  }
}
