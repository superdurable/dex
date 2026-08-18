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
  StepList,
  Wait,
  doubleCodec,
  goTo,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

class StepFirst implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly flow: StepFlow) {}

  public getStepType(): string {
    return "StepFirst";
  }

  public waitFor(context: Context, input: number): Wait {
    context.setStepExecutionLocal("input", input, doubleCodec);
    return Wait.skipImmediately();
  }

  public execute(_context: Context, input: number): StepDecision {
    return goTo(this.flow.secondStep, input + 1);
  }
}

class StepSecond implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "StepSecond";
  }

  public execute(_context: Context, input: number): StepDecision {
    return gracefulComplete(input + 1);
  }
}

export class StepFlow implements Flow<number> {
  private readonly first = new StepFirst(this);
  private readonly second = new StepSecond();

  public get secondStep(): Step<number> {
    return this.second;
  }

  public getFlowType(): string {
    return "StepFlow";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const stepFlow = new StepFlow();
