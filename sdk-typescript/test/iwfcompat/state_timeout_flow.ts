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
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

class StateTimeoutStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "StateTimeoutStep";
  }

  public execute(_context: Context, _input: number): StepDecision {
    throw new Error("timeout simulation");
  }

  public getStepOptions(): StepOptions {
    return { executeMethodTimeoutMs: 1 };
  }
}

export class StateTimeoutFlow implements Flow<number> {
  private readonly start = new StateTimeoutStep();

  public getFlowType(): string {
    return "StateTimeoutFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}
