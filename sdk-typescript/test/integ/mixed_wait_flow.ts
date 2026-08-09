// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

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
    return Wait.until(Timer.byDuration(1_000));
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
