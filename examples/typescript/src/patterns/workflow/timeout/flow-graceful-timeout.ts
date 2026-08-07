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
  StepMovement,
  Timer,
  Wait,
  booleanCodec,
  forceComplete,
  forceFail,
  goToMulti,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

class Init implements Step<boolean> {
  public readonly inputCodec = booleanCodec;

  public constructor(private readonly flow: FlowGracefulTimeout) {}

  public getStepType(): string {
    return "Init";
  }

  public execute(_context: Context, workflowSuccessful: boolean): StepDecision {
    return goToMulti(
      StepMovement.of(this.flow.timeoutStep, undefined),
      StepMovement.of(this.flow.taskStep, workflowSuccessful),
    );
  }
}

class Timeout implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "Timeout";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(Timer.byDuration(60_000));
  }

  public execute(_context: Context, _input: void): StepDecision {
    return forceFail("Workflow did not finish the task in time");
  }
}

class Task implements Step<boolean> {
  public readonly inputCodec = booleanCodec;

  public getStepType(): string {
    return "Task";
  }

  public waitFor(_context: Context, workflowSuccessful: boolean): Wait {
    if (workflowSuccessful) {
      return Wait.skipImmediately();
    }
    return Wait.anyOf(Timer.byDuration(65_000));
  }

  public execute(_context: Context, _workflowSuccessful: boolean): StepDecision {
    return forceComplete("Workflow completed successfully");
  }
}

export class FlowGracefulTimeout implements Flow<boolean> {
  private readonly initStep = new Init(this);
  private readonly timeout = new Timeout();
  private readonly task = new Task();

  public get timeoutStep(): Step<void> {
    return this.timeout;
  }

  public get taskStep(): Step<boolean> {
    return this.task;
  }

  public getFlowType(): string {
    return "FlowGracefulTimeout";
  }

  public getSteps() {
    return StepList.startStep(this.initStep).otherSteps(this.timeout, this.task);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const flowGracefulTimeout = new FlowGracefulTimeout();
