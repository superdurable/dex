// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  StepDef,
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

  public execute(_context: Context, input: number): StepDecision {
    return gracefulComplete(input + 1);
  }
}

class BasicFirstStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly second: BasicSecondStep) {}

  public getStepType(): string {
    return "BasicFirstStep";
  }

  public waitFor(context: Context, input: number): Wait {
    context.setStepExecutionLocal("input", input, doubleCodec);
    return Wait.skipImmediately();
  }

  public execute(_context: Context, input: number): StepDecision {
    return goTo(this.second, input + 1);
  }
}

export class BasicFlow implements Flow<number> {
  private readonly second = new BasicSecondStep();
  private readonly first = new BasicFirstStep(this.second);

  public getFlowType(): string {
    return "BasicFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.first), StepDef.nonStartStep(this.second)];
  }
}
