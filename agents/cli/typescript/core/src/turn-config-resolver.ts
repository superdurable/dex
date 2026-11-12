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
  TurnSessionSettings,
} from "@superdurable/dex-agents";

import type { ResolvedTurn } from "./agent-harness.js";
import { resolveTurn, type EffectiveConfig } from "./cli-config.js";

const maximumCachedTurns = 64;

/**
 * The session and turn of one tool Step: a `DeferredToolExecution`'s `flowId` and `toolCall.turnId`, or a
 * built-in loop `ToolCallContext`'s `flowId` and `turnId`.
 *
 * @since 0.5.0
 */
export interface ToolStepTurn {
  /** The session's Flow ID. */
  readonly flowId: string;
  /** The turn that requested the call. */
  readonly turnId: string;
}

/**
 * Reads a session's committed `TurnSessionSettings` Attribute, or `undefined` when it has none.
 *
 * @since 0.5.0
 */
export type TurnSessionSettingsReader = (flowId: string) => Promise<TurnSessionSettings | undefined>;

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
   * @param readTurnSessionSettings - Reads a session's turn settings, for tool Steps.
   * @param supportedReasoningEfforts - The harness's `AgentHarness.supportedReasoningEfforts`, which a turn's
   * `reasoning_effort` must be one of. Defaults to none, which fails every turn that sets one. Since 0.7.0.
   */
  public constructor(
    private readonly effective: EffectiveConfig,
    private readonly fileInstructions: string | null,
    private readonly reportWarnings: (warnings: readonly string[]) => void,
    private readonly readTurnSessionSettings: TurnSessionSettingsReader,
    private readonly supportedReasoningEfforts: readonly string[] = [],
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
    const { turn, warnings, reasoningEffort } = resolveTurn(
      this.effective,
      sessionSettings,
      this.supportedReasoningEfforts,
    );
    if (warnings.length > 0) {
      this.reportWarnings(warnings);
    }
    const resolved: ResolvedTurn = {
      ...turn,
      instructions: turn.system_prompt ?? (turn.system_prompt_file === null ? null : this.fileInstructions),
      ...(reasoningEffort === undefined ? {} : { reasoningEffort }),
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
   * Returns the permissions of a tool Step: this Worker's file configuration with the settings its turn started
   * with. A tool Step may run on another Worker than the model call that requested it, and outside the model
   * call's scope, so tools check it right before they act. The settings come from the session's committed
   * `TurnSessionSettings`; a copy from another turn reads as no settings, as in the session Flow.
   *
   * @param step - The tool Step's session and turn.
   * @returns The turn's configuration on this Worker.
   * @throws {ConfigError} When the turn's settings fail resolution against this Worker's file.
   */
  public async resolveToolStep(step: ToolStepTurn): Promise<ResolvedTurn> {
    const stored = await this.readTurnSessionSettings(step.flowId);
    return this.resolveTurn(stored !== undefined && stored.turnId === step.turnId ? stored.sessionSettings : {});
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
