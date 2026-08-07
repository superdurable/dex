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
  StepList,
  StepMovement,
  Wait,
  goToMulti,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

class CompleteStep implements Step<string> {
  public readonly inputCodec = stringCodec;
  private output = "";

  public getStepType(): string {
    return "CompleteStep";
  }

  public waitFor(_context: Context, input: string): Wait {
    this.output = `${input}_state2_start`;
    throw new Error("state 2 wait failure");
  }

  public execute(context: Context, _input: string): StepDecision {
    if (!context.waitForMethodFailed()) {
      throw new Error("waitFor failure was not reported");
    }
    this.output += "_state2_decide";
    return gracefulComplete(this.output);
  }

  public getStepOptions(): StepOptions {
    return {
      waitForRetry: { maximumAttempts: 1 },
      waitForFailure: "failFlow",
    };
  }
}

class OverrideFirstStep implements Step<string> {
  public readonly inputCodec = stringCodec;
  private output = "";

  public constructor(private readonly second: CompleteStep) {}

  public getStepType(): string {
    return "OverrideFirstStep";
  }

  public waitFor(_context: Context, input: string): Wait {
    this.output = `${input}_state1_start`;
    return Wait.skipImmediately();
  }

  public execute(_context: Context, _input: string): StepDecision {
    this.output += "_state1_decide";
    return goToMulti(
      StepMovement.of(this.second, this.output, {
        waitForRetry: { maximumAttempts: 2 },
        waitForFailure: "proceed",
      }),
    );
  }
}

export class StateOptionsOverrideFlow implements Flow<string> {
  private readonly second = new CompleteStep();
  private readonly first = new OverrideFirstStep(this.second);

  public getFlowType(): string {
    return "StateOptionsOverrideFlow";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second);
  }
}
