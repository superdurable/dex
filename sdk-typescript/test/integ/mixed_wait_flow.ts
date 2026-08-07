// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  StepList,
  Timer,
  Wait,
  doubleCodec,
  goTo,
  gracefulComplete,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

class MixedTimerStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly options: StepOptions) {}

  public getStepType(): string {
    return "MixedTimerStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.allOf(Timer.byDuration(1_000));
  }

  public execute(_context: Context, input: number): StepDecision {
    return gracefulComplete(input + 1);
  }

  public getStepOptions(): StepOptions {
    return this.options;
  }
}

class MixedImmediateStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly second: MixedTimerStep,
    private readonly options: StepOptions,
  ) {}

  public getStepType(): string {
    return "MixedImmediateStep";
  }

  public execute(_context: Context, input: number): StepDecision {
    return goTo(this.second, input + 1);
  }

  public getStepOptions(): StepOptions {
    return this.options;
  }
}

export class MixedWaitFlow implements Flow<number> {
  private readonly shared: StepOptions = { executeMethodTimeoutMs: 5_000 };
  private readonly second = new MixedTimerStep(this.shared);
  private readonly first = new MixedImmediateStep(this.second, this.shared);

  public getFlowType(): string {
    return "MixedWaitFlow";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second);
  }
}
