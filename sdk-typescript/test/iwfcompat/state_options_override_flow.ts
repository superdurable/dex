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
  StepDef,
  StepMovement,
  goToMulti,
  stringCodec,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

import { CompleteStringStep } from "./shared.js";

class OverrideFirstStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly second: CompleteStringStep) {}

  public getStepType(): string {
    return "OverrideFirstStep";
  }

  public execute(_context: Context, input: string): StepDecision {
    return goToMulti(
      StepMovement.of(this.second, input, {
        waitForMethodTimeoutMs: 2_000,
        executeMethodTimeoutMs: 3_000,
      }),
    );
  }
}

export class StateOptionsOverrideFlow implements Flow<string> {
  private readonly second = new CompleteStringStep();
  private readonly first = new OverrideFirstStep(this.second);

  public getFlowType(): string {
    return "StateOptionsOverrideFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.first), StepDef.nonStartStep(this.second)];
  }
}
