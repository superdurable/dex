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
  goToMany,
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

class WorkA implements Step<string> {
  public readonly inputCodec = stringCodec;
  public getStepType(): string {
    return "WorkA";
  }
  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(`A:${input}`);
  }
}

class WorkB implements Step<string> {
  public readonly inputCodec = stringCodec;
  public getStepType(): string {
    return "WorkB";
  }
  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(`B:${input}`);
  }
}

class StaticInit implements Step<string> {
  public readonly inputCodec = stringCodec;
  public getStepType(): string {
    return "Init";
  }
  public execute(_context: Context, input: string): StepDecision {
    return goToMany(
      StepMovement.of(WorkA, input),
      StepMovement.of(WorkB, input),
    );
  }
}

export class StaticParallelStepsFlow implements Flow<string> {
  private readonly init = new StaticInit();
  private readonly workA = new WorkA();
  private readonly workB = new WorkB();
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

class DynamicDoWork implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "DoWork";
  }
  public async execute(_context: Context, input: number): Promise<StepDecision> {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 50 + Math.floor(Math.random() * 450));
    });
    return gracefulComplete(input);
  }
}

class DynamicInit implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "Init";
  }
  public execute(_context: Context, count: number): StepDecision {
    return goToMany(
      ...Array.from({ length: count }, (_, index) =>
        StepMovement.of(DynamicDoWork, index),
      ),
    );
  }
}

export class DynamicParallelStepsFlow implements Flow<number> {
  private readonly init = new DynamicInit();
  private readonly work = new DynamicDoWork();
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

class AwaitDoWork implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "DoWork";
  }
  public async execute(context: Context, _input: number): Promise<StepDecision> {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 50 + Math.floor(Math.random() * 450));
    });
    completeCh.publish(context, undefined);
    return deadEnd();
  }
}

class Await implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "Await";
  }
  public waitFor(_context: Context, count: number): Wait {
    return Wait.until(completeCh.forN(count));
  }
  public execute(_context: Context, count: number): StepDecision {
    return gracefulComplete(count);
  }
}

class AwaitInit implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "Init";
  }
  public execute(_context: Context, count: number): StepDecision {
    return goToMany(
      StepMovement.of(Await, count),
      ...Array.from({ length: count }, (_, index) =>
        StepMovement.of(AwaitDoWork, index),
      ),
    );
  }
}

export class AwaitParallelStepsFlow implements Flow<number> {
  private readonly init = new AwaitInit();
  private readonly work = new AwaitDoWork();
  private readonly awaitStep = new Await();
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

class FirstWinDoWork implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "DoWork";
  }
  public async execute(_context: Context, input: number): Promise<StepDecision> {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 50 + Math.floor(Math.random() * 450));
    });
    return withCancelingSiblingSteps(gracefulComplete(input), FirstWinDoWork);
  }
}

class FirstWinInit implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "Init";
  }
  public execute(_context: Context, count: number): StepDecision {
    return goToMany(
      ...Array.from({ length: count }, (_, index) =>
        StepMovement.of(FirstWinDoWork, index),
      ),
    );
  }
}

export class FirstWinParallelStepsFlow implements Flow<number> {
  private readonly init = new FirstWinInit();
  private readonly work = new FirstWinDoWork();
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

export const staticParallelStepsFlow = new StaticParallelStepsFlow();
export const dynamicParallelStepsFlow = new DynamicParallelStepsFlow();
export const awaitParallelStepsFlow = new AwaitParallelStepsFlow();
export const firstWinParallelStepsFlow = new FirstWinParallelStepsFlow();
