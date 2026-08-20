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
  Wait,
  doubleCodec,
  goTo,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

const approval = new Channel("Approval", stringCodec);

class ExampleStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly flow: StepFlow) {}

  public getStepType(): string {
    return "ExampleStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.until(approval.forOne());
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
  private readonly example = new ExampleStep(this);
  private readonly second = new StepSecond();

  public get secondStep(): Step<number> {
    return this.second;
  }

  public getFlowType(): string {
    return "StepFlow";
  }

  public getSteps() {
    return StepList.startStep(this.example).otherSteps(this.second);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [approval] };
  }
}

export const stepFlow = new StepFlow();
