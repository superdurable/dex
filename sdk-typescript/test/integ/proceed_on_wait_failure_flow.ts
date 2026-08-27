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
    return goTo(CompleteStringStep, `${input}-recovered`);
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
