// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  StepList,
  doubleCodec,
  goToMulti,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

class EmptyDecisionStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "EmptyDecisionStep";
  }

  public execute(_context: Context, _input: number): StepDecision {
    return goToMulti();
  }

  public getStepOptions(): StepOptions {
    return { executeRetry: { maximumAttempts: 1 } };
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
