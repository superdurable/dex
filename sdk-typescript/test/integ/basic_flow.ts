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
  Wait,
  doubleCodec,
  goTo,
  gracefulComplete,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class BasicSecondStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "BasicSecondStep";
  }

  public async execute(_context: Context, input: number): Promise<StepDecision> {
    await Promise.resolve();
    return gracefulComplete(input + 1);
  }
}

class BasicFirstStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly second: BasicSecondStep) {}

  public getStepType(): string {
    return "BasicFirstStep";
  }

  public async waitFor(context: Context, input: number): Promise<Wait> {
    await Promise.resolve();
    context.setStepExecutionLocal("input", input, doubleCodec);
    return Wait.skipImmediately();
  }

  public async execute(_context: Context, input: number): Promise<StepDecision> {
    await Promise.resolve();
    return goTo(BasicSecondStep, input + 1);
  }
}

export class BasicFlow implements Flow<number> {
  private readonly second = new BasicSecondStep();
  private readonly first = new BasicFirstStep(this.second);

  public getFlowType(): string {
    return "BasicFlow";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second);
  }
}
