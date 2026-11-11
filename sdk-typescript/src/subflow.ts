// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Context } from "./context.js";
import type { Flow } from "./flow.js";
import type { FlowResult } from "./flow-result.js";
import { InvocationContext } from "./invocation-context.js";
import type {
  FlowConfig,
  FlowTimeoutHandlerOptions,
  FlowTimeoutPolicy,
  InitialAttribute,
} from "./options.js";
import type { RetryPolicy } from "./step.js";
import type { Condition } from "./wait.js";

/** Controls how a generated SubFlow Flow ID resolves an existing execution. */
export const SubFlowReusePolicy = Object.freeze({
  /** Attaches to a running execution or returns its existing terminal result. */
  ATTACH: "attach",
  /** Restarts abnormal executions, attaches while running, and returns completed results. */
  RESTART_IF_PREVIOUS_EXITS_ABNORMALLY: "restartIfPreviousExitsAbnormally",
  /** Replaces any different existing execution, including a running one. */
  ALWAYS_RESTART: "alwaysRestart",
} as const);

/** Represents a value from {@link SubFlowReusePolicy}. */
export type SubFlowReusePolicy =
  (typeof SubFlowReusePolicy)[keyof typeof SubFlowReusePolicy];

/** Configures one durable SubFlow condition. All durations use milliseconds. */
export interface SubFlowOptions {
  /** Maximum SubFlow lifetime; normal Flow defaults apply when omitted. */
  readonly timeoutMs?: number;
  /** Action taken when a positive timeout expires; defaults from the target Flow's hook. */
  readonly timeoutPolicy?: FlowTimeoutPolicy;
  /** Execution settings for the target Flow's timeout handler. */
  readonly timeoutHandlerOptions?: FlowTimeoutHandlerOptions;
  /**
   * Delay before the SubFlow starting Step, in milliseconds.
   * The current Server rejects positive values; omit it or use a Timer in the child's starting Step.
   */
  readonly startDelayMs?: number;
  /**
   * Whole-Flow retry behavior after abnormal completion.
   * The current Server rejects any supplied policy; omit it and configure child Step retries.
   */
  readonly retryPolicy?: RetryPolicy;
  /** Initial Attributes owned by the target SubFlow. */
  readonly attributes?: readonly InitialAttribute<any>[];
  /** Fields applied over the inherited parent Flow configuration. */
  readonly configOverride?: FlowConfig;
  /** Existing-execution policy; defaults to abnormal restart. */
  readonly reusePolicy?: SubFlowReusePolicy;
  /** Stable condition ID required by `Wait.anyCombinationOf`. */
  readonly conditionId?: string;
}

/**
 * Creates durable SubFlow conditions and reads their Execute results.
 * Return run() in a Wait from waitFor. Dex starts or attaches to the registered child Flow
 * without occupying a Worker while it waits. A failed child is a terminal FlowResult to inspect;
 * it does not itself throw an application exception in execute.
 * Results and generated Flow IDs use the zero-based declaration order across SubFlow conditions.
 * The generated ID combines the parent Flow ID, Step execution ID, and condition index.
 * Reusing a parent Flow ID or time traveling can therefore address an existing child.
 * Matching generated start Request IDs always attach or return an existing terminal result,
 * regardless of reusePolicy. Otherwise ATTACH preserves the child; ALWAYS_RESTART replaces it.
 * The default restarts abnormal children, attaches to running children, and returns completed results.
 * Wait.anyOf can enter execute while losing children still run; inspect each result before using output.
 *
 * @example
 * ```ts
 * const wait = Wait.until(SubFlow.run(childFlow, input));
 * // In the matching execute handler:
 * const result = SubFlow.getConditionResults(context);
 * const childFlowId = SubFlow.getFlowId(context);
 * ```
 */
export const SubFlow = Object.freeze({
  /**
   * Creates a condition that starts or reuses a registered Flow and awaits completion.
   *
   * Only a Step whose Step type contains no "-" can start SubFlows.
   * The generated child Flow ID joins the parent Flow ID, Step execution ID, and SubFlow index
   * with "-", so a dash in the Step type would let two parents share one child.
   * When a waitFor whose Step type contains "-" returns a SubFlow condition, the server rejects
   * the result as an invalid Worker response.
   * It fails the attempt without retry, so under the default policy the Flow fails.
   * The Worker logs a warning that names the Step type.
   * To fix it, rename the Step type returned by `getStepType`, for example from "b-C" to "bC".
   * Changed in 1.45.0: a Step whose Step type contains "-" can no longer start SubFlows.
   * @typeParam Input - Target starting Step input type.
   * @param flow - Exact target Flow instance registered by the Worker.
   * @param input - Input accepted by its starting Step.
   * @param options - Optional timing, retry, Attribute, configuration, reuse, and ID settings.
   * @returns A durable SubFlow condition accepted by Wait factories.
   */
  run<Input>(flow: Flow<Input>, input: Input, options: SubFlowOptions = {}): Condition {
    return Object.freeze({
      kind: "subFlow" as const,
      subFlow: flow,
      subFlowInput: input,
      subFlowOptions: options,
      ...(options.conditionId === undefined ? {} : { conditionId: options.conditionId }),
    });
  },
  /**
   * Returns one stable-indexed SubFlow result during Step execute.
   * @param context - Current Dex Execute context.
   * @param index - Zero-based SubFlow order; defaults to zero.
   * @returns A terminal result or an AnyOf loser's running snapshot.
   * @throws {TypeError} When called outside a Dex execute Context or a completion lacks output.
   * @throws {RangeError} When no SubFlow result exists at index.
   */
  getConditionResults(context: Context, index = 0): FlowResult {
    return invocation(context).subFlowResult(index);
  },
  /**
   * Returns one generated SubFlow Flow ID during Step execute.
   * @param context - Current Dex Execute context.
   * @param index - Zero-based SubFlow order; defaults to zero.
   * @returns An addressable Flow ID suitable for `Client.stopFlow`.
   * @throws {TypeError} When called outside a Dex execute Context.
   * @throws {RangeError} When no SubFlow result exists at index.
   */
  getFlowId(context: Context, index = 0): string {
    return invocation(context).subFlowId(index);
  },
});

function invocation(context: Context): InvocationContext {
  if (!(context instanceof InvocationContext)) {
    throw new TypeError("SubFlow access requires a Dex invocation Context");
  }
  return context;
}
