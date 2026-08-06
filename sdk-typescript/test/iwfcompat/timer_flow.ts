// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
