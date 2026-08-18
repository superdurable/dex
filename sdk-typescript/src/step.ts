// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import type { AttributeLock } from "./persistence.js";
import type { Channel, ChannelMap, Wait } from "./wait.js";

/** Controls when a Step handler result is durably acknowledged. */
export type StepDurability = "sync" | "async";
/** Controls whether exhausted `waitFor` failures fail the Flow or run `execute`. */
export type WaitForFailurePolicy = "failFlow" | "proceed";

/** Configures exponential retries for a Step handler or Flow. */
export interface RetryPolicy {
  /** Delay before the first retry in milliseconds. */
  readonly initialIntervalMs?: number;
  /** Retry-delay multiplier; uses the server default when omitted. */
  readonly backoffCoefficient?: number;
  /** Upper bound for one retry delay in milliseconds. */
  readonly maximumIntervalMs?: number;
  /** Total attempt limit including the initial call. */
  readonly maximumAttempts?: number;
  /** Overall elapsed-time limit in milliseconds. */
  readonly totalDurationMs?: number;
}

/** Routes exhausted `execute` retries to a fallback Step. */
export interface ExecuteFailure {
  /** Registered fallback Step. */
  readonly step: Step<unknown>;
  /** Options applied to the fallback movement. */
  readonly options?: StepOptions;
}

/** Creates execute-failure routing settings. */
export const ExecuteFailure = Object.freeze({
  /**
   * Routes exhausted execution retries to another Step.
   * @typeParam Input - Fallback Step input type.
   * @param step - Registered fallback Step.
   * @param options - Optional options for the fallback movement.
   * @returns The failure-routing settings.
   */
  proceedTo<Input>(step: Step<Input>, options?: StepOptions): ExecuteFailure {
    return {
      step: step as Step<unknown>,
      ...(options === undefined ? {} : { options }),
    };
  },
});

/** Configures timeouts, retries, durability, locks, and failure routing for a Step. */
export interface StepOptions {
  /** Maximum duration of one `waitFor` attempt in milliseconds. */
  readonly waitForMethodTimeoutMs?: number;
  /** Maximum duration of one `execute` attempt in milliseconds. */
  readonly executeMethodTimeoutMs?: number;
  /** Regular-activity heartbeat in milliseconds; must represent whole seconds. */
  readonly heartbeatTimeoutMs?: number;
  /** Optional retry policy for `waitFor`. */
  readonly waitForRetry?: RetryPolicy;
  /** Optional retry policy for `execute`. */
  readonly executeRetry?: RetryPolicy;
  /** Behavior after all `waitFor` attempts fail. */
  readonly waitForFailure?: WaitForFailurePolicy;
  /** Durability used for the `waitFor` result. */
  readonly waitForDurability?: StepDurability;
  /** Durability used for the `execute` result. */
  readonly executeDurability?: StepDurability;
  /** Attribute locks held during `waitFor`. */
  readonly waitForLockAttributes?: readonly AttributeLock[];
  /** Attribute locks held during `execute`. */
  readonly executeLockAttributes?: readonly AttributeLock[];
  /** Fallback routing after exhausted `execute` retries. */
  readonly executeFailure?: ExecuteFailure;
}

/**
 * Defines one typed unit of durable Flow application logic.
 *
 * @example
 * ```ts
 * const confirm: Step<{ id: string }> = {
 *   getStepType: () => "Confirm",
 *   waitFor: () => Wait.until(Timer.byDuration(1_000)),
 *   execute: (_context, input) => forceComplete(input),
 * };
 * ```
 * @typeParam Input - Value passed by an incoming Step movement.
 */
export interface Step<Input> {
  /**
   * Codec used for every incoming input value.
   *
   * Omit this for JSON objects. Scalar wire kinds still need {@link stringCodec},
   * {@link booleanCodec}, {@link int64Codec}, {@link doubleCodec}, or
   * {@link bytesCodec}.
   */
  readonly inputCodec?: Codec<Input>;
  /**
   * Returns the protocol Step type.
   * @returns A non-empty Step type unique within the containing Flow.
   */
  getStepType(): string;
  /**
   * Returns this Step's static options.
   * @returns Static options, or `undefined` for Flow and server defaults.
   */
  getStepOptions?(): StepOptions | undefined;
  /**
   * Describes durable conditions required before execution.
   * @param context - Current execution metadata and persistence operations.
   * @param input - Decoded Step input.
   * @returns A Wait or promise resolving to one.
   */
  waitFor?(context: Context, input: Input): Wait | Promise<Wait>;
  /**
   * Runs application logic and produces the next durable decision.
   * @param context - Current execution metadata and persistence operations.
   * @param input - Decoded Step input.
   * @returns A StepDecision or promise resolving to one.
   */
  execute(context: Context, input: Input): StepDecision | Promise<StepDecision>;
}

interface StartStepDefinition<StartInput> {
  readonly step: Step<StartInput>;
  readonly isStartStep: true;
}

interface NonStartStepDefinition {
  readonly step: Step<unknown>;
  readonly isStartStep: false;
}

type StepDefinition = StartStepDefinition<unknown> | NonStartStepDefinition;

declare const startInputType: unique symbol;

/**
 * Builds the ordered Step definitions for a Flow.
 * @typeParam StartInput - Input type of the optional starting Step.
 */
export class StepList<StartInput> {
  /** Compile-time brand preserving the starting input type. */
  public declare readonly [startInputType]: (input: StartInput) => StartInput;

  private readonly definitions: readonly StepDefinition[];

  private constructor(definitions: readonly StepDefinition[]) {
    this.definitions = Object.freeze([...definitions]);
  }

  /**
   * Creates a StepList with no Steps.
   * @typeParam StartInput - Declared Flow start input type.
   * @returns An empty StepList.
   */
  public static empty<StartInput = void>(): StepList<StartInput> {
    return new StepList([]);
  }

  /**
   * Creates a StepList with one starting Step.
   * @typeParam StartInput - Starting Step input type.
   * @param step - Step receiving `startFlow` input.
   * @returns A StepList with exactly one starting Step.
   */
  public static startStep<StartInput>(step: Step<StartInput>): StepList<StartInput> {
    return new StepList([{ step: step as Step<unknown>, isStartStep: true }]);
  }

  /**
   * Creates a StepList containing only non-starting Steps.
   * @typeParam StartInput - Declared start input; defaults to `never`.
   * @param steps - Steps reachable through decisions, RPCs, or failure routing.
   * @returns A StepList without a starting Step.
   */
  public static withoutStartStep<StartInput = never>(
    ...steps: readonly Step<any>[]
  ): StepList<StartInput> {
    return new StepList(steps.map((step) => ({ step: step as Step<unknown>, isStartStep: false })));
  }

  /**
   * Returns a copy with non-starting Steps appended.
   * @param steps - Registered Steps appended in argument order.
   * @returns A new StepList.
   */
  public otherSteps(...steps: readonly Step<any>[]): StepList<StartInput> {
    return new StepList([
      ...this.definitions,
      ...steps.map((step) => ({ step: step as Step<unknown>, isStartStep: false as const })),
    ]);
  }

  /**
   * Iterates Step definitions in registration order.
   * @returns An iterator over the registered Step definitions.
   */
  public [Symbol.iterator](): Iterator<StepDefinition> {
    return this.definitions[Symbol.iterator]();
  }
}

/**
 * Schedules one registered Step with typed input and optional options.
 * @typeParam Input - Destination Step input type.
 */
export interface StepMovement<Input> {
  /** Registered destination Step. */
  readonly step: Step<Input>;
  /** Value encoded for the destination Step. */
  readonly input: Input;
  /** Optional per-movement Step options. */
  readonly options?: StepOptions;
}

/** Creates typed Step movements. */
export const StepMovement = Object.freeze({
  /**
   * Creates a movement to one Step.
   * @typeParam Input - Destination Step input type.
   * @param step - Registered destination Step.
   * @param input - Typed destination input.
   * @param options - Optional per-movement options.
   * @returns The new Step movement.
   */
  of<Input>(step: Step<Input>, input: Input, options?: StepOptions): StepMovement<Input> {
    return {
      step,
      input,
      ...(options === undefined ? {} : { options }),
    };
  },
});

/** Selects queued or active Step executions canceled by a decision. */
export interface StepCancellationSelection {
  /** Registered Step types canceled across the current Flow. */
  readonly cancelingSteps?: readonly Step<any>[];
  /** Registered Step types canceled only when they share the current scheduling source. */
  readonly cancelingSiblingSteps?: readonly Step<any>[];
}

/** Describes the durable transition returned by `Step.execute`. */
export type StepDecision = (
  | Readonly<{
      /** Schedules next Step movements. */
      kind: "next";
      /** Ordered destination movements. */
      movements: readonly StepMovement<unknown>[];
    }>
  | Readonly<{
      /** Requests graceful or immediate successful completion. */
      kind: "gracefulComplete" | "forceComplete";
      /** Codec-supported Flow result. */
      output: unknown;
    }>
  | Readonly<{
      /** Completes only when selected Channels are empty. */
      kind: "forceCompleteIfChannelsEmpty";
      /** Codec-supported Flow result. */
      output: unknown;
      /** Movement scheduled when any selected Channel is non-empty. */
      fallback: StepMovement<unknown>;
      /** Registered Channels inspected for emptiness. */
      channels: readonly (Channel<unknown> | ChannelMap<unknown>)[];
    }>
  | Readonly<{
      /** Requests immediate Flow failure. */
      kind: "forceFail";
      /** Application-readable failure detail. */
      reason: string;
    }>
  | Readonly<{
      /** Ends this path without scheduling work or closing the Flow. */
      kind: "deadEnd";
    }>) & Readonly<StepCancellationSelection>;

/**
 * Creates a decision scheduling one next Step.
 * @typeParam Input - Destination Step input type.
 * @param step - Registered destination Step.
 * @param input - Typed destination input.
 * @returns A next decision containing one movement.
 */
export const goTo = <Input>(step: Step<Input>, input: Input): StepDecision =>
  goToMulti(StepMovement.of(step, input));

/**
 * Creates a decision scheduling several next Steps.
 * @param movements - Typed movements applied in argument order.
 * @returns A next decision containing every movement.
 */
export const goToMulti = (...movements: readonly StepMovement<unknown>[]): StepDecision => ({
  kind: "next",
  movements,
});

/**
 * Requests successful completion after already-scheduled Steps finish.
 * @param output - Optional codec-supported Flow result.
 * @returns A graceful-completion decision.
 */
export const gracefulComplete = (output?: unknown): StepDecision => ({
  kind: "gracefulComplete",
  output,
});

/**
 * Requests immediate successful completion.
 * @param output - Optional codec-supported Flow result.
 * @returns A force-completion decision.
 */
export const forceComplete = (output?: unknown): StepDecision => ({ kind: "forceComplete", output });

/**
 * Completes only when selected Channels are empty, otherwise schedules a fallback.
 * @param output - Codec-supported completion result.
 * @param fallback - Movement used when any selected Channel is non-empty.
 * @param channels - Registered Channel definitions to inspect.
 * @returns A conditional force-completion decision.
 */
export const forceCompleteIfChannelsEmpty = (
  output: unknown,
  fallback: StepMovement<unknown>,
  ...channels: readonly (Channel<unknown> | ChannelMap<unknown>)[]
): StepDecision => ({ kind: "forceCompleteIfChannelsEmpty", output, fallback, channels });

/**
 * Requests immediate Flow failure.
 * @param reason - Non-empty application-readable failure detail.
 * @returns A force-failure decision.
 */
export const forceFail = (reason: string): StepDecision => ({ kind: "forceFail", reason });

/**
 * Creates a dead-end decision.
 * @returns A decision ending this path without closing the Flow.
 */
export const deadEnd = (): StepDecision => ({ kind: "deadEnd" });

/**
 * Returns a copy selecting every current execution of the supplied Step types.
 *
 * Dex resolves one snapshot after `execute` succeeds. Finished, already-canceled,
 * and absent executions are no-ops. Steps scheduled by the same decision are excluded.
 * Repeated calls take the union, and Flow-wide selection supersedes sibling selection.
 * @param decision - Decision to copy.
 * @param steps - Exact Step instances registered with the current Flow.
 * @returns A new decision containing the combined Flow-wide selectors.
 */
export const withCancelingSteps = (
  decision: StepDecision,
  ...steps: readonly Step<any>[]
): StepDecision => {
  const cancelingSteps = unionSteps(decision.cancelingSteps, steps);
  const cancelingSiblingSteps = (decision.cancelingSiblingSteps ?? []).filter(
    (step) => !cancelingSteps.includes(step),
  );
  return { ...decision, cancelingSteps, cancelingSiblingSteps };
};

/**
 * Returns a copy selecting same-source executions of the supplied Step types.
 *
 * A sibling has the same `Context.fromStepExecutionId` as the execution returning
 * the decision. Snapshot and no-op behavior match {@link withCancelingSteps}.
 * @param decision - Decision to copy.
 * @param steps - Exact Step instances registered with the current Flow.
 * @returns A new decision containing the combined sibling selectors.
 */
export const withCancelingSiblingSteps = (
  decision: StepDecision,
  ...steps: readonly Step<any>[]
): StepDecision => ({
  ...decision,
  cancelingSiblingSteps: unionSteps(decision.cancelingSiblingSteps, steps).filter(
    (step) => !(decision.cancelingSteps ?? []).includes(step),
  ),
});

function unionSteps(
  existing: readonly Step<any>[] | undefined,
  added: readonly Step<any>[],
): readonly Step<any>[] {
  const combined = [...(existing ?? [])];
  for (const step of added) {
    if (!combined.includes(step)) {
      combined.push(step);
    }
  }
  return combined;
}
