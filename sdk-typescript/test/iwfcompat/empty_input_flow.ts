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
  StepDef,
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
    return [StepDef.startStep(this.first), StepDef.nonStartStep(this.second)];
  }
}
