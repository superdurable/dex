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
  forceFail,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class ForceFailStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ForceFailStep";
  }

  public execute(_context: Context, _input: number): StepDecision {
    return forceFail("a failing message");
  }
}

export class ForceFailFlow implements Flow<number> {
  private readonly start = new ForceFailStep();

  public getFlowType(): string {
    return "ForceFailFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.start)];
  }
}
