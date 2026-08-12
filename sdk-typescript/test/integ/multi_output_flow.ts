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
  goToMulti,
  gracefulComplete,
  voidCodec,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

export class MultiOutputStringStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "MultiOutputStringStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("branch-one");
  }
}

export class MultiOutputNumberStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "MultiOutputNumberStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete(42);
  }
}

class MultiOutputStartStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(
    private readonly stringStep: MultiOutputStringStep,
    private readonly numberStep: MultiOutputNumberStep,
  ) {}

  public getStepType(): string {
    return "MultiOutputStartStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goToMulti(
      StepMovement.of(this.stringStep, undefined),
      StepMovement.of(this.numberStep, undefined),
    );
  }
}

export class MultiOutputFlow implements Flow<void> {
  public readonly stringStep = new MultiOutputStringStep();
  public readonly numberStep = new MultiOutputNumberStep();
  private readonly start = new MultiOutputStartStep(this.stringStep, this.numberStep);

  public getFlowType(): string {
    return "MultiOutputFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.stringStep, this.numberStep);
  }
}
