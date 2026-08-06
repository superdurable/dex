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
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class StateFailureStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "StateFailureStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.skipImmediately();
  }

  public execute(_context: Context, _input: number): StepDecision {
    throw new Error("state API failure");
  }
}

export class StateFailureFlow implements Flow<number> {
  private readonly start = new StateFailureStep();

  public getFlowType(): string {
    return "StateFailureFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.start)];
  }
}
