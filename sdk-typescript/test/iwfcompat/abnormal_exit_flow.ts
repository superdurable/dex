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
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class AbnormalExitStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "AbnormalExitStep";
  }

  public execute(_context: Context, _input: number): StepDecision {
    throw new Error("abnormal exit");
  }
}

export class AbnormalExitFlow implements Flow<number> {
  private readonly start = new AbnormalExitStep();

  public getFlowType(): string {
    return "AbnormalExitFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.start)];
  }
}
