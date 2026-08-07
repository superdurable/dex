// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  StepList,
  Wait,
  goTo,
  stringCodec,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

import { CompleteStringStep } from "./shared.js";

class FailingWaitStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly second: CompleteStringStep) {}

  public getStepType(): string {
    return "FailingWaitStep";
  }

  public waitFor(_context: Context, _input: string): Wait {
    throw new Error("wait failure");
  }

  public execute(_context: Context, input: string): StepDecision {
    return goTo(this.second, `${input}-recovered`);
  }

  public getStepOptions(): StepOptions {
    return {
      waitForFailure: "proceed",
      waitForRetry: { maximumAttempts: 2 },
    };
  }
}

export class ProceedOnWaitFailureFlow implements Flow<string> {
  private readonly second = new CompleteStringStep();
  private readonly first = new FailingWaitStep(this.second);

  public getFlowType(): string {
    return "ProceedOnWaitFailureFlow";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second);
  }
}
