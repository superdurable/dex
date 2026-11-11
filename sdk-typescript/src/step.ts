// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Codec } from "./codec.js";
import type { AsyncContext, Context } from "./context.js";
import type { InvalidStepResultError } from "./errors.js";
import type { AttributeLock, AttributeMap, AttributeMapLoad } from "./persistence.js";
import type { Channel, ChannelMap, ChannelMapLoad, Wait } from "./wait.js";
import { requireName } from "./validation.js";

/**
 * Controls when a Step handler result is durably acknowledged.
 * Async gives lower latency and faster single Flows; sustained throughput is about the same as sync.
 * `sync` persists the attempt start before hand-out and the result before advancing the Flow.
 * `async` hands out work and advances before persistence; commits are batched, normally within 20 ms.
 * A server failure can replay a recent result and work started from it.
 * If an async start was not persisted, the same attempt number can run again without consuming a retry.
 * Those executions can overlap, observe different input, and repeat Stream writes under the same source.
 * Both modes use the same retry, method timeout, heartbeat timeout, and checkpoint settings.
 * Cancellation is cooperative, so even sync attempts can overlap with retries or recovery handlers.
 * Keep external effects idempotent. Prefer sync for costly or replay-sensitive operations.
 * Durability is fixed when a method becomes pending; configuration changes affect later methods only.
 * Changed in SDK v1.35.0: attempt starts follow the hand-out and persistence rules above.
 */
export type StepDurability = "sync" | "async";
/** Controls whether exhausted `waitFor` failures fail the Flow or run `execute`. */
export type WaitForFailurePolicy = "failFlow" | "proceed";

/**
 * Configures exponential retries for a Step handler or Flow.
 *
 * Step methods default to 1 second initially, multiplier 2, maximum interval 100 seconds,
 * unlimited attempts within a four-hour retry duration. Set maximumAttempts to 1 to disable retries.
 * `waitFor` and `execute` have independent policies and one-based attempt counts.
 * Duration fields use milliseconds but must be nonnegative whole seconds; invalid durations
 * raise RangeError when the SDK maps the options. Zero uses the server default.
 * A positive retryAfter delay from a handler overrides the next backoff interval.
 * The type is also accepted by Flow and SubFlow start options for source compatibility,
 * but the current Dex Server rejects a supplied Flow retry policy. Omit it and use Step retries.
 *
 * @example
 * ```ts
 * const retry: RetryPolicy = {
 *   initialIntervalMs: 1_000,
 *   backoffCoefficient: 2,
 *   maximumIntervalMs: 30_000,
 *   maximumAttempts: 5,
 *   totalDurationMs: 10 * 60_000,
 * };
 * const options: StepOptions = { executeRetry: retry, executeMethodTimeoutMs: 60_000 };
 * ```
 */
export interface RetryPolicy {
  /** Delay before the first retry in milliseconds; omitted or zero defaults to 1,000. */
  readonly initialIntervalMs?: number;
  /** Retry-delay multiplier; omitted or zero defaults to 2. Each delay is capped by maximumIntervalMs. */
  readonly backoffCoefficient?: number;
  /** Upper bound for one retry delay in milliseconds; omitted or zero defaults to 100,000. */
  readonly maximumIntervalMs?: number;
  /** Total attempt limit including the initial call; omitted or zero imposes no attempt-count limit. */
  readonly maximumAttempts?: number;
  /**
   * Retry elapsed-time limit in milliseconds; defaults to four hours when omitted.
   * For a Step, counted from the first hand-out to a Worker.
   * Checked only after an attempt fails to decide whether another retry can be scheduled.
   * It never ends a running Step attempt; use the method timeout to bound an attempt.
   * Returning attempt 1 without running resets the clock. Initial queue and lock waits do not count.
   * Later queue waits do count, but Dex does not recheck the limit while a retry is queued.
   * A retry whose backoff fits the budget can therefore start or finish after the duration expires.
   * After a long queue wait, its failure may leave no budget for another retry.
   * Changed in SDK v1.35.0: requires Server protocol 4; earlier servers could shorten an attempt
   * deadline to the remaining retry duration. Upgrade the Server before the SDK.
   */
  readonly totalDurationMs?: number;
}

/** Routes exhausted `execute` retries to a fallback Step. */
export interface ExecuteFailure {
  /** Fallback class, registered instance, or StepRef; validated at registration. */
  readonly step: StepTarget<any>;
  /** Options applied to the fallback movement. */
  readonly options?: StepOptions;
}

/** Creates execute-failure routing settings. */
export const ExecuteFailure = Object.freeze({
  /**
   * Routes exhausted execution retries to another Step.
   *
   * The fallback Step receives the failing Step's input. Registry rejects a fallback whose
   * built-in scalar input codec differs from the failing Step's. Other codecs are not compared.
   * Changed in SDK v1.28.0: accepts registered instances and StepRef as well as Step classes.
   * @typeParam Input - Fallback Step input type.
   * @param step - Fallback class, registered instance, or StepRef; validated at registration.
   * @param options - Optional options for the fallback movement.
   * @returns The failure-routing settings.
   */
  proceedTo<Input>(step: StepTarget<Input>, options?: StepOptions): ExecuteFailure {
    return {
      step: step as StepTarget<any>,
      ...(options === undefined ? {} : { options }),
    };
  },
});

/**
 * Configures timeouts, retries, durability, locks, and failure routing for a Step.
 *
 * Omitted or zero timeouts use two hours per method attempt and one minute without a heartbeat.
 * Both durability modes use these defaults. Durations must be nonnegative whole seconds.
 * Timeouts begin at hand-out to a Worker, excluding queue and lock waits.
 * A timed-out handler receives cancellation; it must cooperate with cancellationSignal.
 * Returning a Wait ends the waitFor method attempt; waiting on its conditions does not occupy a Worker.
 * A retry reuses the first attempt's loaded Attribute and Channel snapshot, including selections.
 * It does not see later writes. Wait on a Channel in a subsequent Step to observe new state.
 * Attribute locks remain held across retries, backoff, and returned tasks until the method ends.
 * Conflicting Steps wait; an RPC that requests a held lock reports RpcLockConflictError.
 * Keep lock scope narrow and retry budgets bounded.
 * Singleton Attributes are loaded automatically; select AttributeMaps and pending Channel messages explicitly.
 * Omitted waitForFailure fails the Flow; `proceed` runs execute, which can inspect waitForMethodFailed().
 * Without executeFailure, exhausted execute retries fail the Flow.
 * Changed in SDK v1.35.0: unavailable Workers or unsupported Steps wait durably without consuming retries.
 * A handed-out interrupted attempt consumes a retry, except an unpersisted async start as in StepDurability.
 *
 * @example
 * ```ts
 * const options: StepOptions = {
 *   executeMethodTimeoutMs: 120_000,
 *   heartbeatTimeoutMs: 120_000,
 *   executeRetry: { maximumAttempts: 3, totalDurationMs: 600_000 },
 *   executeDurability: "sync",
 * };
 * ```
 */
export interface StepOptions {
  /** Regular `waitFor` attempt limit in milliseconds; defaults to two hours. */
  readonly waitForMethodTimeoutMs?: number;
  /** Regular `execute` attempt limit in milliseconds; defaults to two hours. */
  readonly executeMethodTimeoutMs?: number;
  /**
   * Regular heartbeat timeout in milliseconds; requires whole seconds and defaults to one minute.
   * The server's default minimum positive value is 10,000 ms; smaller values are rejected.
   * Record explicit heartbeats or write Stream frames before this interval expires in either mode.
   * Only explicit heartbeat values checkpoint progress; a Stream frame preserves the last checkpoint.
   * Raise this interval for healthy external calls that can remain silent longer than one minute.
   * The method timeout still bounds the whole attempt.
   */
  readonly heartbeatTimeoutMs?: number;
  /** Optional retry policy for `waitFor`. */
  readonly waitForRetry?: RetryPolicy;
  /** Optional retry policy for `execute`. */
  readonly executeRetry?: RetryPolicy;
  /** Behavior after all `waitFor` attempts fail. */
  readonly waitForFailure?: WaitForFailurePolicy;
  /** WaitFor durability override; otherwise uses Flow configuration, then sync. */
  readonly waitForDurability?: StepDurability;
  /** Execute durability override; otherwise uses Flow configuration, then sync. */
  readonly executeDurability?: StepDurability;
  /** Attribute locks held during `waitFor`. */
  readonly waitForLockAttributes?: readonly AttributeLock[];
  /** Attribute locks held during `execute`. */
  readonly executeLockAttributes?: readonly AttributeLock[];
  /** AttributeMap definitions whose complete contents are loaded for `waitFor`. */
  readonly waitForLoadAttributeMaps?: readonly AttributeMap<unknown>[];
  /** Exact AttributeMap instances loaded for `waitFor`. */
  readonly waitForLoadAttributeMapInstances?: readonly AttributeMapLoad[];
  /** Channels whose pending messages are loaded for `waitFor`. */
  readonly waitForLoadChannels?: readonly Channel<unknown>[];
  /** ChannelMap definitions whose complete pending contents are loaded for `waitFor`. */
  readonly waitForLoadChannelMaps?: readonly ChannelMap<unknown>[];
  /** Exact ChannelMap instances whose pending messages are loaded for `waitFor`. */
  readonly waitForLoadChannelMapInstances?: readonly ChannelMapLoad[];
  /** AttributeMap definitions whose complete contents are loaded for `execute`. */
  readonly executeLoadAttributeMaps?: readonly AttributeMap<unknown>[];
  /** Exact AttributeMap instances loaded for `execute`. */
  readonly executeLoadAttributeMapInstances?: readonly AttributeMapLoad[];
  /** Channels whose pending messages are loaded for `execute`. */
  readonly executeLoadChannels?: readonly Channel<unknown>[];
  /** ChannelMap definitions whose complete pending contents are loaded for `execute`. */
  readonly executeLoadChannelMaps?: readonly ChannelMap<unknown>[];
  /** Exact ChannelMap instances whose pending messages are loaded for `execute`. */
  readonly executeLoadChannelMapInstances?: readonly ChannelMapLoad[];
  /** Fallback routing after exhausted `execute` retries. */
  readonly executeFailure?: ExecuteFailure;
}

/**
 * Defines one typed unit of durable Flow application logic.
 *
 * @example
 * ```ts
 * class Confirm implements Step<{ id: string }> {
 *   public getStepType(): string { return "Confirm"; }
 *   public waitFor(): Wait { return Wait.until(Timer.byDuration(1_000)); }
 *   public execute(_context: Context, input: { id: string }): StepDecision {
 *     return forceComplete(input);
 *   }
 * }
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
   *
   * A Step type that starts SubFlows must not contain "-".
   * Dex joins the parts of a generated child Flow ID with "-", so a dash makes that ID ambiguous.
   * Such a Step fails its waitFor with an {@link InvalidStepResultError} when it returns a SubFlow
   * condition. Step types that never start SubFlows may still contain "-".
   * Changed in 1.44.0: a Step whose Step type contains "-" can no longer start SubFlows.
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
   * Async handlers that record heartbeats must accept AsyncContext and return a Promise.
   * @param context - Current execution metadata and persistence operations.
   * @param input - Decoded Step input.
   * @returns A Wait or promise resolving to one.
   */
  waitFor?: StepHandler<Input, Wait>;
  /**
   * Runs application logic and produces the next durable decision.
   * Async handlers that record heartbeats must accept AsyncContext and return a Promise.
   * @param context - Current execution metadata and persistence operations.
   * @param input - Decoded Step input.
   * @returns A StepDecision or promise resolving to one.
   */
  execute: StepHandler<Input, StepDecision>;
}

type StepHandler<Input, Output> =
  | ((context: Context, input: Input) => Output | Promise<Output>)
  | ((context: AsyncContext, input: Input) => Promise<Output>);

/**
 * Identifies one registered Step implementation by its runtime class.
 * @typeParam Input - Value accepted by the Step implementation.
 */
export type StepClass<Input> = abstract new (...arguments_: any[]) => Step<Input>;

/**
 * Addresses a registered Step by its stable Step type without an implementation.
 *
 * Use with goTo, StepMovement, failure routing, or cancellation. References cannot
 * execute or appear in a StepList. Registry validates execute-failure routes declared
 * by getStepOptions. Client validates timeout-handler failure routes when it maps
 * start options. A StepRef returned from a handler, in a movement or cancellation
 * selection, is validated only when the Worker applies that returned result. The
 * constructor never checks that the Step type exists. Invalid declarations raise
 * FlowDefinitionError. Invalid handler targets are reported as InvalidStepResultError.
 * The destination codec validates input at runtime.
 * @example
 * ```ts
 * const movement = StepMovement.of(new StepRef("Charge"), { orderId: "42" });
 * ```
 * @since 1.28.0
 */
export class StepRef {
  /**
   * Creates a reference to a Step type registered in the containing Flow.
   * @param stepType - Non-empty type returned by the destination's getStepType.
   * @throws TypeError If stepType is empty or whitespace-only.
   */
  public constructor(
    /** Stable destination Step type. */
    public readonly stepType: string,
  ) {
    requireName(stepType);
    Object.freeze(this);
  }
}

/**
 * Selects a Step by class, registered instance, or stable Step type reference.
 * Class targets require exactly one registered instance. When a class is
 * ambiguous, use its registered instance or a StepRef instead.
 * @typeParam Input - Destination input; StepRef inputs are checked at runtime.
 * @since 1.28.0
 */
export type StepTarget<Input> = StepClass<Input> | Step<Input> | StepRef;

interface StartStepDefinition<StartInput> {
  readonly step: Step<StartInput>;
  readonly isStartStep: true;
}

interface NonStartStepDefinition {
  readonly step: Step<any>;
  readonly isStartStep: false;
}

type StepDefinition = StartStepDefinition<unknown> | NonStartStepDefinition;

declare const startInputType: unique symbol;

/**
 * Builds the ordered Step definitions for a Flow.
 * Changed in SDK v1.28.0: distinct instances of one Step class can be registered with unique Step types.
 * Address an ambiguous class by its registered instance or a StepRef.
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
    return new StepList([{ step: step as Step<any>, isStartStep: true }]);
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
    return new StepList(steps.map((step) => ({ step: step as Step<any>, isStartStep: false })));
  }

  /**
   * Returns a copy with non-starting Steps appended.
   * @param steps - Registered Steps appended in argument order.
   * @returns A new StepList.
   */
  public otherSteps(...steps: readonly Step<any>[]): StepList<StartInput> {
    return new StepList([
      ...this.definitions,
      ...steps.map((step) => ({ step: step as Step<any>, isStartStep: false as const })),
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
 * Changed in SDK v1.28.0: accepts registered instances and StepRef as well as Step classes.
 * @typeParam Input - Destination Step input type.
 */
export interface StepMovement<Input> {
  /**
   * Destination class, registered instance, or StepRef in this Flow.
   * Code that reads this field must narrow it before using class members.
   */
  readonly step: StepTarget<Input>;
  /** Value encoded for the destination Step. */
  readonly input: Input;
  /** Optional per-movement Step options. */
  readonly options?: StepOptions;
}

/** Creates typed Step movements. */
export const StepMovement = Object.freeze({
  /**
   * Creates a movement to one Step.
   *
   * The Worker validates the target when it applies the handler result that returns
   * this movement. An unknown StepRef, ambiguous class, or unregistered instance then
   * fails that result with InvalidStepResultError.
   * Changed in SDK v1.28.0: accepts registered instances and StepRef as well as Step classes.
   * @typeParam Input - Destination Step input type.
   * @param step - Destination class, registered instance, or StepRef in this Flow.
   * @param input - Typed destination input.
   * @param options - Optional per-movement options.
   * @returns The new Step movement.
   */
  of<Input>(step: StepTarget<Input>, input: Input, options?: StepOptions): StepMovement<Input> {
    return {
      step,
      input,
      ...(options === undefined ? {} : { options }),
    };
  },
});

/** Selects queued or active Step executions canceled by a decision. */
export interface StepCancellationSelection {
  /**
   * Registered Step types canceled across the current Flow.
   * Entries are classes, registered instances, or StepRef values; narrow them before use.
   */
  readonly cancelingSteps?: readonly StepTarget<any>[];
  /**
   * Registered Step types canceled only when they share the current scheduling source.
   * Entries are classes, registered instances, or StepRef values; narrow them before use.
   */
  readonly cancelingSiblingSteps?: readonly StepTarget<any>[];
}

/** Describes the durable transition returned by `Step.execute`. */
export type StepDecision = (
  | Readonly<{
      /** Schedules next Step movements. */
      kind: "next";
      /** Ordered destination movements. */
      movements: readonly StepMovement<any>[];
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
      fallback: StepMovement<any>;
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
 *
 * The Worker validates the target when it applies the returned decision. An unknown
 * StepRef, ambiguous class, or unregistered instance fails the handler result with
 * InvalidStepResultError.
 * Changed in SDK v1.28.0: accepts registered instances and StepRef as well as Step classes.
 * @typeParam Input - Destination Step input type.
 * @param step - Registered destination class, instance, or StepRef.
 * @param input - Typed destination input.
 * @returns A next decision containing one movement.
 */
export const goTo = <Input>(step: StepTarget<Input>, input: Input): StepDecision =>
  goToMany(StepMovement.of(step, input));

/**
 * Creates a decision scheduling several next Steps.
 * @param movements - Typed movements applied in argument order.
 * @returns A next decision containing every movement.
 */
export const goToMany = (...movements: readonly StepMovement<any>[]): StepDecision => ({
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
  fallback: StepMovement<any>,
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
 * The Worker validates each target when it applies the returned decision; an invalid
 * target fails the handler result with InvalidStepResultError.
 * Changed in SDK v1.28.0: accepts registered instances and StepRef as well as Step classes.
 * @param decision - Decision to copy.
 * @param steps - Registered instances, StepRef values, or unambiguous Step classes.
 * @returns A new decision containing the combined Flow-wide selectors.
 */
export const withCancelingSteps = (
  decision: StepDecision,
  ...steps: readonly StepTarget<any>[]
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
 * Changed in SDK v1.28.0: accepts registered instances and StepRef as well as Step classes.
 * @param decision - Decision to copy.
 * @param steps - Registered instances, StepRef values, or unambiguous Step classes.
 * @returns A new decision containing the combined sibling selectors.
 */
export const withCancelingSiblingSteps = (
  decision: StepDecision,
  ...steps: readonly StepTarget<any>[]
): StepDecision => ({
  ...decision,
  cancelingSiblingSteps: unionSteps(decision.cancelingSiblingSteps, steps).filter(
    (step) => !(decision.cancelingSteps ?? []).includes(step),
  ),
});

function unionSteps(
  existing: readonly StepTarget<any>[] | undefined,
  added: readonly StepTarget<any>[],
): readonly StepTarget<any>[] {
  const combined = [...(existing ?? [])];
  for (const step of added) {
    if (!combined.includes(step)) {
      combined.push(step);
    }
  }
  return combined;
}
