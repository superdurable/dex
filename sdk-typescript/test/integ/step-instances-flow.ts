// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  ExecuteFailure, StepList, StepMovement, StepRef, Wait, goTo, goToMany,
  gracefulComplete, doubleCodec, stringCodec, voidCodec,
  withCancelingSteps, withCancelingSiblingSteps,
  type Codec, type Context, type Flow, type Step, type StepDecision,
  type StepOptions, type StepTarget,
} from "../../src/index.js";

export class TypedInstanceStep<Input> implements Step<Input> {
  public constructor(
    private readonly stepType: string,
    public readonly inputCodec: Codec<Input>,
    private readonly decide: (input: Input) => StepDecision,
  ) {}

  public getStepType(): string { return this.stepType; }
  public waitFor(_context: Context, _input: Input): Wait { return Wait.skipImmediately(); }
  public execute(_context: Context, input: Input): StepDecision { return this.decide(input); }
}

export class StepInstancesFlow implements Flow<number> {
  public readonly numbers = new TypedInstanceStep("Numbers", doubleCodec, this.advanceNumber.bind(this));
  public readonly strings = new TypedInstanceStep("Strings", stringCodec, this.advanceString.bind(this));
  public readonly observed: (number | string)[] = [];

  public getFlowType(): string { return "StepInstancesFlow"; }
  public getSteps(): StepList<number> {
    return StepList.startStep(this.numbers).otherSteps(this.strings);
  }

  private advanceNumber(input: number): StepDecision {
    if (typeof input !== "number") throw new TypeError("expected number input");
    this.observed.push(input);
    if (input === 0) return goTo(this.strings, "first");
    if (input === 1) return goToMany(StepMovement.of(new StepRef("Strings"), "second"));
    return withCancelingSiblingSteps(
      withCancelingSteps(gracefulComplete(input), this.strings, new StepRef("Strings")),
      this.numbers, new StepRef("Numbers"),
    );
  }

  private advanceString(input: string): StepDecision {
    if (typeof input !== "string") throw new TypeError("expected string input");
    this.observed.push(input);
    if (input === "first") return goTo(new StepRef("Numbers"), 1);
    return goToMany(StepMovement.of(this.numbers, 2));
  }
}

export class FailingInstanceStep implements Step<void> {
  public readonly inputCodec = voidCodec;
  public constructor(private readonly target: StepTarget<void>) {}
  public getStepType(): string { return "FailingInstanceStep"; }
  public getStepOptions(): StepOptions {
    return {
      executeRetry: { maximumAttempts: 1 },
      executeFailure: ExecuteFailure.proceedTo(this.target),
    };
  }
  public execute(_context: Context, _input: void): StepDecision {
    throw new Error("route exhausted execution to recovery");
  }
}

export class InstanceRecoveryFlow implements Flow<void> {
  private readonly recovery = new TypedInstanceStep("Recovery", voidCodec, this.complete);
  private readonly failure: FailingInstanceStep;
  public constructor(reference: boolean) {
    this.failure = new FailingInstanceStep(reference ? new StepRef("Recovery") : this.recovery);
  }
  public getFlowType(): string { return "InstanceRecoveryFlow"; }
  public getSteps(): StepList<void> {
    return StepList.startStep(this.failure).otherSteps(this.recovery);
  }
  private complete(input: void): StepDecision {
    if (input !== undefined) throw new TypeError("expected no recovery input");
    return gracefulComplete("recovered");
  }
}
