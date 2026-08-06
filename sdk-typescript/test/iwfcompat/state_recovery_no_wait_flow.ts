// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  ExecuteFailure,
  StepDef,
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

class RecoverNoWaitStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "RecoverNoWaitStep";
  }

  public execute(_context: Context, input: number): StepDecision {
    return gracefulComplete(input * 2);
  }
}

class FailingNoWaitStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly recover: RecoverNoWaitStep) {}

  public getStepType(): string {
    return "FailingNoWaitStep";
  }

  public execute(_context: Context, _input: number): StepDecision {
    throw new Error("execute failure");
  }

  public getStepOptions(): StepOptions {
    return { executeFailure: ExecuteFailure.proceedTo(this.recover) };
  }
}

export class StateRecoveryNoWaitFlow implements Flow<number> {
  private readonly recover = new RecoverNoWaitStep();
  private readonly start = new FailingNoWaitStep(this.recover);

  public getFlowType(): string {
    return "StateRecoveryNoWaitFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.start), StepDef.nonStartStep(this.recover)];
  }
}
