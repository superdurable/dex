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
  LongPollTimeoutError,
  StepList,
  StepMovement,
  Timer,
  Wait,
  doubleCodec,
  goTo,
  goToMulti,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { getClient } from "../../client-holder.js";
import { startOptions } from "../../config/env.js";
import { isFlowAlreadyStarted } from "../../service-errors.js";
import { childFlow, type ChildFlow } from "../scalable-parallel/child-flow.js";
import { type WaitForChildInput } from "./wait-for-child-input.js";

export const CONCURRENCY_PER_PARENT_WORKFLOW = 3;
export const TASK_QUEUE = "task_queue";

const taskQueue = new Channel(TASK_QUEUE, doubleCodec);

const countInputCodec = doubleCodec;

class Init implements Step<number> {
  public readonly inputCodec = countInputCodec;

  public constructor(private readonly flow: ParentFlowV2) {}

  public getStepType(): string {
    return "Init";
  }

  public execute(context: Context, numRequests: number): StepDecision {
    for (let index = 0; index < numRequests; index += 1) {
      taskQueue.publish(context, index);
    }

    const movements: StepMovement<unknown>[] = [];
    for (let index = 0; index < CONCURRENCY_PER_PARENT_WORKFLOW; index += 1) {
      movements.push(StepMovement.of(this.flow.loopForNextTaskStep, undefined));
    }
    return goToMulti(...movements);
  }
}

class LoopForNextTask implements Step<void> {
  public constructor(private readonly flow: ParentFlowV2) {}

  public getStepType(): string {
    return "LoopForNextTask";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(taskQueue.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const request = taskQueue.results(context)[0];
    if (request === undefined) {
      throw new Error("No task found on queue");
    }
    return goTo(this.flow.startChildWorkflowStep, request);
  }
}

class StartChildWorkflow implements Step<number> {
  public readonly inputCodec = countInputCodec;

  public constructor(
    private readonly flow: ParentFlowV2,
    private readonly child: ChildFlow,
  ) {}

  public getStepType(): string {
    return "StartChildWorkflow";
  }

  public async execute(_context: Context, uuid: number): Promise<StepDecision> {
    const childWorkflowId = `child-wf-${uuid}`;
    try {
      await getClient().startFlow(this.child, childWorkflowId, String(uuid), startOptions());
    } catch (error) {
      if (isFlowAlreadyStarted(error)) {
        console.log("ignore this error because it is already started");
      } else {
        throw error;
      }
    }
    return goTo(this.flow.awaitChildWorkflowCompletionStep, {
      childWFId: childWorkflowId,
      timerSeconds: 1,
    });
  }
}

class AwaitChildWorkflowCompletion implements Step<WaitForChildInput> {
  public constructor(private readonly flow: ParentFlowV2) {}

  public getStepType(): string {
    return "AwaitChildWorkflowCompletion";
  }

  public waitFor(_context: Context, input: WaitForChildInput): Wait {
    return Wait.until(Timer.byDuration(input.timerSeconds * 1000));
  }

  public async execute(
    _context: Context,
    input: WaitForChildInput,
  ): Promise<StepDecision> {
    try {
      await getClient().waitForFlow(
        input.childWFId,
        Math.max(input.timerSeconds, 1) * 1000,
      );
    } catch (error) {
      if (error instanceof LongPollTimeoutError) {
        return goTo(this.flow.awaitChildWorkflowCompletionStep, {
          childWFId: input.childWFId,
          timerSeconds: Math.min(input.timerSeconds * 2, 10),
        });
      }
      throw error;
    }
    return goTo(this.flow.loopForNextTaskStep, undefined);
  }
}

export class ParentFlowV2 implements Flow<number> {
  private readonly initStep = new Init(this);
  private readonly loopForNextTask = new LoopForNextTask(this);
  private readonly startChildWorkflow = new StartChildWorkflow(this, childFlow);
  private readonly awaitChildWorkflowCompletion = new AwaitChildWorkflowCompletion(this);

  public get loopForNextTaskStep(): Step<void> {
    return this.loopForNextTask;
  }

  public get startChildWorkflowStep(): Step<number> {
    return this.startChildWorkflow;
  }

  public get awaitChildWorkflowCompletionStep(): Step<WaitForChildInput> {
    return this.awaitChildWorkflowCompletion;
  }

  public getFlowType(): string {
    return "ParentFlowV2";
  }

  public getSteps() {
    return StepList.startStep(this.initStep).otherSteps(
      this.loopForNextTask,
      this.startChildWorkflow,
      this.awaitChildWorkflowCompletion,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [taskQueue] };
  }
}

export const parentFlowV2 = new ParentFlowV2();
