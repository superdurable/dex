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
    return [StepDef.startStep(this.start)];
  }
}
