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
    return StepList.startStep(this.first).otherSteps(this.second);
  }
}
