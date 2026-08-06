// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  StepDef,
  doubleCodec,
  goTo,
  gracefulComplete,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class ExecuteOnlySecondStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ExecuteOnlySecondStep";
  }

  public execute(_context: Context, input: number): StepDecision {
    return gracefulComplete(input + 1);
  }
}

class ExecuteOnlyFirstStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly second: ExecuteOnlySecondStep) {}

  public getStepType(): string {
    return "ExecuteOnlyFirstStep";
  }

  public execute(_context: Context, input: number): StepDecision {
    return goTo(this.second, input + 1);
  }
}

export class ExecuteOnlyFlow implements Flow<number> {
  private readonly second = new ExecuteOnlySecondStep();
  private readonly first = new ExecuteOnlyFirstStep(this.second);

  public getFlowType(): string {
    return "ExecuteOnlyFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.first), StepDef.nonStartStep(this.second)];
  }
}
