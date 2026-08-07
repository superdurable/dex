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
  Timer,
  Wait,
  goTo,
  gracefulComplete,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

class SimplePolling implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: SimplePollingFlow) {}

  public getStepType(): string {
    return "SimplePolling";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(Timer.byDuration(10_000));
  }

  public execute(_context: Context, _input: void): StepDecision {
    if (this.isSystemReady()) {
      return goTo(this.flow.simplePollingCompleteStep, undefined);
    }
    return goTo(this.flow.simplePollingStep, undefined);
  }

  private isSystemReady(): boolean {
    console.log("Executing external system check for readiness...");
    return true;
  }
}

class SimplePollingComplete implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "SimplePollingComplete";
  }

  public execute(_context: Context, _input: void): StepDecision {
    console.log("Executing final state to complete the workflow...");
    return gracefulComplete();
  }
}

export class SimplePollingFlow implements Flow<void> {
  private readonly simplePolling = new SimplePolling(this);
  private readonly simplePollingComplete = new SimplePollingComplete();

  public get simplePollingStep(): Step<void> {
    return this.simplePolling;
  }

  public get simplePollingCompleteStep(): Step<void> {
    return this.simplePollingComplete;
  }

  public getFlowType(): string {
    return "SimplePollingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.simplePolling).otherSteps(this.simplePollingComplete);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const simplePollingFlow = new SimplePollingFlow();
