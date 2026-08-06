// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
