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
  Timer,
  Wait,
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class TimerStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "TimerStep";
  }

  public waitFor(_context: Context, input: number): Wait {
    return Wait.allOf(Timer.byDuration(input * 1_000, "test-timer-id"));
  }

  public execute(_context: Context, _input: number): StepDecision {
    return gracefulComplete();
  }
}

export class TimerFlow implements Flow<number> {
  private readonly start = new TimerStep();

  public getFlowType(): string {
    return "TimerFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.start)];
  }
}
