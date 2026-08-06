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
  goToMulti,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class EmptyDecisionStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "EmptyDecisionStep";
  }

  public execute(_context: Context, _input: number): StepDecision {
    return goToMulti();
  }
}

export class EmptyDecisionFlow implements Flow<number> {
  private readonly start = new EmptyDecisionStep();

  public getFlowType(): string {
    return "EmptyDecisionFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}
