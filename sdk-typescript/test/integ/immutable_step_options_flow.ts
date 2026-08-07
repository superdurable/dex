// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  StepList,
  StepMovement,
  Wait,
  doubleCodec,
  goTo,
  goToMulti,
  gracefulComplete,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

class ImmutableFailingWaitStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ImmutableFailingWaitStep";
  }

  public waitFor(_context: Context, input: number): Wait {
    throw new Error(`expected wait failure ${input}`);
  }

  public execute(context: Context, input: number): StepDecision {
    if (!context.waitForMethodFailed()) {
      throw new Error("wait failure was not reported");
    }
    return input === 1 ? goTo(this, 2) : gracefulComplete(input);
  }

  public getStepOptions(): StepOptions {
    return {
      waitForRetry: { maximumAttempts: 1 },
      waitForFailure: "failFlow",
    };
  }
}

class ImmutableStartStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly failing: ImmutableFailingWaitStep) {}

  public getStepType(): string {
    return "ImmutableStartStep";
  }

  public execute(_context: Context, _input: number): StepDecision {
    return goToMulti(
      StepMovement.of(this.failing, 1, {
        waitForRetry: { maximumAttempts: 1 },
        waitForFailure: "proceed",
      }),
    );
  }
}

export class ImmutableStepOptionsFlow implements Flow<number> {
  private readonly failing = new ImmutableFailingWaitStep();
  private readonly start = new ImmutableStartStep(this.failing);

  public getFlowType(): string {
    return "ImmutableStepOptionsFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.failing);
  }
}
