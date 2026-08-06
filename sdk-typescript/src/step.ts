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

export type StepDurability = "sync" | "async";
export type WaitForFailurePolicy = "failFlow" | "proceed";

export interface RetryPolicy {
  readonly initialIntervalMs?: number;
  readonly backoffCoefficient?: number;
  readonly maximumIntervalMs?: number;
  readonly maximumAttempts?: number;
  readonly totalDurationMs?: number;
}

export interface ExecuteFailure {
  readonly step: Step<unknown>;
  readonly options?: StepOptions;
}

export const ExecuteFailure = Object.freeze({
  proceedTo<Input>(step: Step<Input>, options?: StepOptions): ExecuteFailure {
    return {
      step: step as Step<unknown>,
      ...(options === undefined ? {} : { options }),
    };
  },
});

export interface StepOptions {
  readonly waitForMethodTimeoutMs?: number;
  readonly executeMethodTimeoutMs?: number;
  readonly waitForRetry?: RetryPolicy;
  readonly executeRetry?: RetryPolicy;
  readonly waitForFailure?: WaitForFailurePolicy;
  readonly waitForDurability?: StepDurability;
  readonly executeDurability?: StepDurability;
  readonly waitForLockAttributes?: readonly AttributeLock[];
  readonly executeLockAttributes?: readonly AttributeLock[];
  readonly executeFailure?: ExecuteFailure;
}

export interface Step<Input> {
  readonly inputCodec: Codec<Input>;
  getStepType(): string;
  getStepOptions?(): StepOptions | undefined;
  waitFor?(context: Context, input: Input): Wait;
  execute(context: Context, input: Input): StepDecision;
}

export interface StartStepDef<StartInput> {
  readonly step: Step<StartInput>;
  readonly isStartStep: true;
}

export interface NonStartStepDef {
  readonly step: Step<unknown>;
  readonly isStartStep: false;
}

export type StepDef = StartStepDef<unknown> | NonStartStepDef;

export const StepDef = Object.freeze({
  startStep<StartInput>(step: Step<StartInput>): StartStepDef<StartInput> {
    return { step, isStartStep: true };
  },
  nonStartStep<Input>(step: Step<Input>): NonStartStepDef {
    return { step: step as Step<unknown>, isStartStep: false };
  },
});

export interface StepMovement<Input> {
  readonly step: Step<Input>;
  readonly input: Input;
  readonly options?: StepOptions;
}

export const StepMovement = Object.freeze({
  of<Input>(step: Step<Input>, input: Input, options?: StepOptions): StepMovement<Input> {
    return {
      step,
      input,
      ...(options === undefined ? {} : { options }),
    };
  },
});

export type StepDecision =
  | Readonly<{ kind: "next"; movements: readonly StepMovement<unknown>[] }>
  | Readonly<{ kind: "gracefulComplete" | "forceComplete"; output: unknown }>
  | Readonly<{
      kind: "forceCompleteIfChannelsEmpty";
      output: unknown;
      fallback: StepMovement<unknown>;
      channels: readonly (Channel<unknown> | ChannelMap<unknown>)[];
    }>
  | Readonly<{ kind: "forceFail"; reason: string }>
  | Readonly<{ kind: "deadEnd" }>;

export const goTo = <Input>(step: Step<Input>, input: Input): StepDecision =>
  goToMulti(StepMovement.of(step, input));

export const goToMulti = (...movements: readonly StepMovement<unknown>[]): StepDecision => ({
  kind: "next",
  movements,
});

export const gracefulComplete = (output?: unknown): StepDecision => ({
  kind: "gracefulComplete",
  output,
});

export const forceComplete = (output?: unknown): StepDecision => ({ kind: "forceComplete", output });

export const forceCompleteWhenChannelsEmpty = (
  output: unknown,
  fallback: StepMovement<unknown>,
  ...channels: readonly (Channel<unknown> | ChannelMap<unknown>)[]
): StepDecision => ({ kind: "forceCompleteIfChannelsEmpty", output, fallback, channels });

export const forceFail = (reason: string): StepDecision => ({ kind: "forceFail", reason });

export const deadEnd = (): StepDecision => ({ kind: "deadEnd" });
