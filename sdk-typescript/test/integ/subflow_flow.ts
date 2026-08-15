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
  SubFlow,
  Timer,
  Wait,
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type FlowConfig,
  type Step,
  type StepDecision,
  type SubFlowOptions,
  type SubFlowReusePolicy,
  StepList,
} from "../../src/index.js";

class SingleSubFlowStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly target: Flow<number>,
    private readonly options: SubFlowOptions = {},
  ) {}

  public getStepType(): string {
    return "ParentStep";
  }

  public waitFor(_context: Context, input: number): Wait {
    return Wait.until(SubFlow.run(this.target, input, this.options));
  }

  public execute(context: Context, _input: number): StepDecision {
    const result = SubFlow.getConditionResults(context);
    const output = result.completions.length === 0 ? "" : result.singleOutput(doubleCodec);
    return gracefulComplete(
      `${SubFlow.getFlowId(context)}|${result.status}|${output}`,
    );
  }
}

export class SingleSubFlowParent implements Flow<number> {
  public readonly start: SingleSubFlowStep;

  public constructor(target: Flow<number>, reusePolicy?: SubFlowReusePolicy) {
    this.start = new SingleSubFlowStep(
      target,
      reusePolicy === undefined ? {} : { reusePolicy },
    );
  }

  public getFlowType(): string {
    return "SingleSubFlowParent";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}

class AllSubFlowStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly target: Flow<number>) {}

  public getStepType(): string {
    return "ParentStep";
  }

  public waitFor(_context: Context, input: number): Wait {
    return Wait.allOf(
      SubFlow.run(this.target, input),
      SubFlow.run(this.target, input + 10),
    );
  }

  public execute(context: Context, _input: number): StepDecision {
    return gracefulComplete([0, 1].map((index) => {
      const result = SubFlow.getConditionResults(context, index);
      return `${SubFlow.getFlowId(context, index)}|${result.status}|${result.singleOutput(doubleCodec)}`;
    }).join(";"));
  }
}

export class AllSubFlowParent implements Flow<number> {
  public readonly start: AllSubFlowStep;

  public constructor(target: Flow<number>) {
    this.start = new AllSubFlowStep(target);
  }

  public getFlowType(): string {
    return "AllSubFlowParent";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}

class AnySubFlowStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly target: Flow<number>) {}

  public getStepType(): string {
    return "ParentStep";
  }

  public waitFor(_context: Context, input: number): Wait {
    return Wait.anyOf(Timer.byDuration(0), SubFlow.run(this.target, input));
  }

  public execute(context: Context, _input: number): StepDecision {
    const result = SubFlow.getConditionResults(context);
    let rejectedOutput = false;
    try {
      result.singleOutput(doubleCodec);
    } catch (error) {
      if (!(error instanceof TypeError)) throw error;
      rejectedOutput = true;
    }
    return gracefulComplete(
      `${SubFlow.getFlowId(context)}|${result.status}|${result.isTerminal}|${rejectedOutput}`,
    );
  }
}

export class AnySubFlowParent implements Flow<number> {
  public readonly start: AnySubFlowStep;

  public constructor(target: Flow<number>) {
    this.start = new AnySubFlowStep(target);
  }

  public getFlowType(): string {
    return "AnySubFlowParent";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}

class ContinueAsNewSubFlowStep implements Step<number> {
  public readonly inputCodec = doubleCodec;
  private readonly options: SubFlowOptions = {
    configOverride: { continueAsNewThreshold: 100 } satisfies FlowConfig,
  };

  public constructor(
    private readonly completed: Flow<number>,
    private readonly delayed: Flow<number>,
  ) {}

  public getStepType(): string {
    return "ParentStep";
  }

  public waitFor(_context: Context, input: number): Wait {
    return Wait.allOf(
      SubFlow.run(this.completed, input, this.options),
      SubFlow.run(this.delayed, 300, this.options),
    );
  }

  public execute(context: Context, _input: number): StepDecision {
    const completed = SubFlow.getConditionResults(context);
    const delayed = SubFlow.getConditionResults(context, 1);
    return gracefulComplete(
      `${SubFlow.getFlowId(context)}|${completed.singleOutput(doubleCodec)}|`
      + `${SubFlow.getFlowId(context, 1)}|${delayed.status}`,
    );
  }
}

export class ContinueAsNewSubFlowParent implements Flow<number> {
  public readonly start: ContinueAsNewSubFlowStep;

  public constructor(completed: Flow<number>, delayed: Flow<number>) {
    this.start = new ContinueAsNewSubFlowStep(completed, delayed);
  }

  public getFlowType(): string {
    return "ContinueAsNewSubFlowParent";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}
