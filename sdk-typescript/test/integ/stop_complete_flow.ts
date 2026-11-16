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
  StepList,
  deadEnd,
  doubleCodec,
  type AsyncContext,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class StopCompleteBlockingStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly flow: StopCompleteFlow) {}

  public getStepType(): string {
    return "StopCompleteBlockingStep";
  }

  public async execute(_context: AsyncContext, _input: number): Promise<StepDecision> {
    this.flow.markExecuteStarted();
    await Promise.race([
      this.flow.executeReleased,
      new Promise((resolve) => setTimeout(resolve, 20_000).unref()),
    ]);
    return deadEnd();
  }
}

export class StopCompleteFlow implements Flow<number> {
  public readonly executeStarted: Promise<void>;
  public readonly executeReleased: Promise<void>;
  private resolveExecuteStarted!: () => void;
  private resolveExecuteReleased!: () => void;
  private readonly start = new StopCompleteBlockingStep(this);

  public constructor() {
    this.executeStarted = new Promise((resolve) => { this.resolveExecuteStarted = resolve; });
    this.executeReleased = new Promise((resolve) => { this.resolveExecuteReleased = resolve; });
  }

  public getFlowType(): string {
    return "StopCompleteFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public markExecuteStarted(): void {
    this.resolveExecuteStarted();
  }

  public releaseExecute(): void {
    this.resolveExecuteReleased();
  }
}
