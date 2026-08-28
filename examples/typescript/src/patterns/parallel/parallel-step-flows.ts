/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import {
  Channel,
  StepList,
  StepMovement,
  Wait,
  deadEnd,
  doubleCodec,
  goToMulti,
  gracefulComplete,
  stringCodec,
  voidCodec,
  withCancelingSiblingSteps,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

class WorkAStep implements Step<string> {
  public readonly inputCodec = stringCodec;
  public getStepType(): string {
    return "WorkAStep";
  }
  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(`A:${input}`);
  }
}

class WorkBStep implements Step<string> {
  public readonly inputCodec = stringCodec;
  public getStepType(): string {
    return "WorkBStep";
  }
  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(`B:${input}`);
  }
}

class StaticInitStep implements Step<string> {
  public readonly inputCodec = stringCodec;
  public getStepType(): string {
    return "InitStep";
  }
  public execute(_context: Context, input: string): StepDecision {
    return goToMulti(
      StepMovement.of(WorkAStep, input),
      StepMovement.of(WorkBStep, input),
    );
  }
}

export class StaticParallelStepsFlow implements Flow<string> {
  private readonly init = new StaticInitStep();
  private readonly workA = new WorkAStep();
  private readonly workB = new WorkBStep();
  public getFlowType(): string {
    return "StaticParallelStepsFlow";
  }
  public getSteps() {
    return StepList.startStep(this.init).otherSteps(this.workA, this.workB);
  }
  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

class DynamicDoWorkStep implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "DoWorkStep";
  }
  public async execute(_context: Context, input: number): Promise<StepDecision> {
    await randomDelay();
    return gracefulComplete(input);
  }
}

class DynamicInitStep implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "InitStep";
  }
  public execute(_context: Context, count: number): StepDecision {
    return goToMulti(...movements(count, DynamicDoWorkStep));
  }
}

export class DynamicParallelStepsFlow implements Flow<number> {
  private readonly init = new DynamicInitStep();
  private readonly work = new DynamicDoWorkStep();
  public getFlowType(): string {
    return "DynamicParallelStepsFlow";
  }
  public getSteps() {
    return StepList.startStep(this.init).otherSteps(this.work);
  }
  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

const completeCh = new Channel("parallel-complete", voidCodec);

class AwaitDoWorkStep implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "DoWorkStep";
  }
  public async execute(context: Context, _input: number): Promise<StepDecision> {
    await randomDelay();
    completeCh.publish(context, undefined);
    return deadEnd();
  }
}

class AwaitStep implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "AwaitStep";
  }
  public waitFor(_context: Context, count: number): Wait {
    return Wait.until(completeCh.forN(count));
  }
  public execute(_context: Context, count: number): StepDecision {
    return gracefulComplete(count);
  }
}

class AwaitInitStep implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "InitStep";
  }
  public execute(_context: Context, count: number): StepDecision {
    return goToMulti(
      StepMovement.of(AwaitStep, count),
      ...movements(count, AwaitDoWorkStep),
    );
  }
}

export class AwaitParallelStepsFlow implements Flow<number> {
  private readonly init = new AwaitInitStep();
  private readonly work = new AwaitDoWorkStep();
  private readonly awaitStep = new AwaitStep();
  public getFlowType(): string {
    return "AwaitParallelStepsFlow";
  }
  public getSteps() {
    return StepList.startStep(this.init).otherSteps(this.work, this.awaitStep);
  }
  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [completeCh] };
  }
}

class FirstWinDoWorkStep implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "DoWorkStep";
  }
  public async execute(_context: Context, input: number): Promise<StepDecision> {
    await randomDelay();
    return withCancelingSiblingSteps(gracefulComplete(input), FirstWinDoWorkStep);
  }
}

class FirstWinInitStep implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "InitStep";
  }
  public execute(_context: Context, count: number): StepDecision {
    return goToMulti(...movements(count, FirstWinDoWorkStep));
  }
}

export class FirstWinParallelStepsFlow implements Flow<number> {
  private readonly init = new FirstWinInitStep();
  private readonly work = new FirstWinDoWorkStep();
  public getFlowType(): string {
    return "FirstWinParallelStepsFlow";
  }
  public getSteps() {
    return StepList.startStep(this.init).otherSteps(this.work);
  }
  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

function movements(count: number, step: new () => Step<number>) {
  return Array.from({ length: count }, (_, index) => StepMovement.of(step, index));
}

async function randomDelay(): Promise<void> {
  await new Promise<void>((resolve) => {
    setTimeout(resolve, Math.floor(Math.random() * 500));
  });
}

export const staticParallelStepsFlow = new StaticParallelStepsFlow();
export const dynamicParallelStepsFlow = new DynamicParallelStepsFlow();
export const awaitParallelStepsFlow = new AwaitParallelStepsFlow();
export const firstWinParallelStepsFlow = new FirstWinParallelStepsFlow();
