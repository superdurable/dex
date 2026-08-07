// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  StepList,
  goTo,
  gracefulComplete,
  voidCodec,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class EmptySecondStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "EmptySecondStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete();
  }
}

class EmptyFirstStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly second: EmptySecondStep) {}

  public getStepType(): string {
    return "EmptyFirstStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(this.second, undefined);
  }
}

export class EmptyInputFlow implements Flow {
  private readonly second = new EmptySecondStep();
  private readonly first = new EmptyFirstStep(this.second);

  public getFlowType(): string {
    return "test-customized-flow-type";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second);
  }
}
