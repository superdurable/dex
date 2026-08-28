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
  Attribute,
  Channel,
  StepList,
  StepMovement,
  Timer,
  Wait,
  deadEnd,
  doubleCodec,
  goTo,
  goToMany,
  gracefulComplete,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import {
  myDependencyService,
  type MyDependencyService,
} from "../../shared/my-dependency-service.js";

export const TASK_A_COMPLETED = "task-a-completed";
export const TASK_B_COMPLETED = "task-b-completed";
export const TASK_C_COMPLETED = "task-c-completed";

const POLL_INTERVAL_MS = 1_000;

export const taskACompleted = new Channel(TASK_A_COMPLETED, voidCodec);
export const taskBCompleted = new Channel(TASK_B_COMPLETED, voidCodec);
export const taskCCompleted = new Channel(TASK_C_COMPLETED, voidCodec);

export class PollingFlow implements Flow<number> {
  public readonly currentPolls = new Attribute("current-polls", doubleCodec);

  public readonly initialize = new Initialize(this);
  public readonly poll = new Poll(this);
  public readonly waitForTasks = new WaitForTasks(this);

  public constructor(public readonly service: MyDependencyService = myDependencyService) {}

  public getFlowType(): string {
    return "PollingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initialize).otherSteps(this.poll, this.waitForTasks);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.currentPolls],
      channels: [taskACompleted, taskBCompleted, taskCCompleted],
    };
  }
}

class Initialize implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly flow: PollingFlow) {}

  public getStepType(): string {
    return "Initialize";
  }

  public execute(context: Context, maximumPolls: number): StepDecision {
    this.flow.currentPolls.set(context, 0);
    return goToMany(
      StepMovement.of(Poll, maximumPolls),
      StepMovement.of(WaitForTasks, undefined),
    );
  }
}

class WaitForTasks implements Step<void> {
  public constructor(private readonly flow: PollingFlow) {}

  public getStepType(): string {
    return "WaitForTasks";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.allOf(
      taskACompleted.forOne(),
      taskBCompleted.forOne(),
      taskCCompleted.forOne(),
    );
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("all tasks completed");
  }
}

class Poll implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly flow: PollingFlow) {}

  public getStepType(): string {
    return "Poll";
  }

  public waitFor(_context: Context, _maximumPolls: number): Wait {
    return Wait.until(Timer.byDuration(POLL_INTERVAL_MS));
  }

  public execute(context: Context, maximumPolls: number): StepDecision {
    this.flow.service.callAPI1("calling API1 for polling service C");
    const polls = this.flow.currentPolls.get(context);
    if (polls >= maximumPolls) {
      taskCCompleted.publish(context, undefined);
      return deadEnd();
    }
    this.flow.currentPolls.set(context, polls + 1);
    return goTo(Poll, maximumPolls);
  }
}

export const pollingFlow = new PollingFlow();
