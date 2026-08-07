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
  ChannelMap,
  ErrorSubStatus,
  IdReusePolicy,
  InitialAttribute,
  StepList,
  StepMovement,
  Wait,
  booleanCodec,
  forceCompleteWhenChannelsEmpty,
  goTo,
  jsonCodec,
  rpc,
  stringCodec,
  voidCodec,
  type Condition,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { HOUR_MS } from "../../../config/env.js";
import { startFlowSync, SyncClientError } from "../../client-sync.js";
import {
  batchEnqueueRequestCodec,
  type BatchEnqueueRequest,
} from "./models/batch-enqueue-request.js";
import { childFlow, type ChildFlow } from "./child-flow.js";

export const NUM_PARENT_WORKFLOWS = 2;
export const CONCURRENCY_PER_PARENT_WORKFLOW = 3;
export const MAX_BUFFERED_TASKS = 10;
export const TASK_QUEUE = "TaskQueue";
export const CHILD_COMPLETE_CHANNEL_PREFIX = "ChildComplete_";
export const DA_CURRENT_WAIT_CHILD_WFS = "CurrentWaitChildWfs";

const batchInputCodec = jsonCodec<BatchEnqueueRequest>(batchEnqueueRequestCodec);
const stringArrayCodec = jsonCodec<string[]>({
  typeName: "string[]",
  decode: (value: unknown) =>
    Array.isArray(value) ? value.map(String) : [],
});

class Init implements Step<BatchEnqueueRequest> {
  public readonly inputCodec = batchInputCodec;

  public constructor(private readonly flow: ParentFlow) {}

  public getStepType(): string {
    return "Init";
  }

  public execute(context: Context, initRequest: BatchEnqueueRequest): StepDecision {
    for (const uuid of initRequest.list) {
      this.flow.taskQueue.publish(context, uuid);
    }
    return goTo(this.flow.loopForNextMessageStep, undefined);
  }
}

class LoopForNextMessage implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(
    private readonly flow: ParentFlow,
    private readonly child: ChildFlow,
  ) {}

  public getStepType(): string {
    return "LoopForNextMessage";
  }

  public waitFor(context: Context, _input: void): Wait {
    let waiting = this.flow.currentWaitChildWfs.get(context);
    if (waiting === undefined) {
      waiting = [];
    }

    const conditions: Condition[] = [];
    if (waiting.length < CONCURRENCY_PER_PARENT_WORKFLOW) {
      conditions.push(this.flow.taskQueue.forOne());
    }
    for (const childWfId of waiting) {
      conditions.push(this.flow.childComplete.forOne(childWfId));
    }
    return Wait.anyOf(...conditions);
  }

  public execute(context: Context, _input: void): StepDecision {
    let waiting = this.flow.currentWaitChildWfs.get(context) ?? [];
    const newWaitList = [...waiting];

    const taskResults = this.flow.taskQueue.results(context);
    if (taskResults.length > 0) {
      const request = taskResults[0];
      if (request === undefined) {
        throw new Error("No task result found");
      }
      const childWorkflowId = `processing-${request}`;
      try {
        startFlowSync({
          flowType: this.child.getFlowType(),
          flowId: childWorkflowId,
          input: request,
          options: {
            timeoutMs: HOUR_MS,
            ignoreAlreadyStarted: true,
            requestId: context.stepExecutionId,
            idReusePolicy: IdReusePolicy.DISALLOW,
            attributes: [
              InitialAttribute.of(this.child.parentWorkflowId, context.flowId),
            ],
          },
        });
        newWaitList.push(childWorkflowId);
      } catch (error) {
        if (
          error instanceof SyncClientError &&
          error.subStatus === ErrorSubStatus.FLOW_ALREADY_STARTED
        ) {
          console.log(
            "already started by other state/workflow, ignore it -- not waiting for it",
          );
        } else {
          throw error;
        }
      }
    }

    for (const childWfId of [...newWaitList]) {
      if (this.flow.childComplete.results(context, childWfId).length > 0) {
        const exists = newWaitList.splice(newWaitList.indexOf(childWfId), 1).length > 0;
        if (!exists) {
          throw new Error(`child workflow ${childWfId} is not in the waiting list?`);
        }
      }
    }

    this.flow.currentWaitChildWfs.set(context, newWaitList);

    if (newWaitList.length === 0) {
      return forceCompleteWhenChannelsEmpty(
        null,
        StepMovement.of(this.flow.loopForNextMessageStep, undefined),
        this.flow.taskQueue,
      );
    }
    return goTo(this.flow.loopForNextMessageStep, undefined);
  }
}

export class ParentFlow implements Flow<BatchEnqueueRequest> {
  public readonly taskQueue = new Channel(TASK_QUEUE, stringCodec);
  public readonly childComplete = new ChannelMap(CHILD_COMPLETE_CHANNEL_PREFIX, voidCodec);
  public readonly currentWaitChildWfs = new Attribute(
    DA_CURRENT_WAIT_CHILD_WFS,
    stringArrayCodec,
  );

  private readonly initStep = new Init(this);
  private readonly loopForNextMessage = new LoopForNextMessage(this, childFlow);

  public get loopForNextMessageStep(): Step<void> {
    return this.loopForNextMessage;
  }

  public getFlowType(): string {
    return "ParentFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initStep).otherSteps(this.loopForNextMessage);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.currentWaitChildWfs],
      channels: [this.taskQueue, this.childComplete],
    };
  }

  @rpc({ inputCodec: batchInputCodec, outputCodec: booleanCodec })
  public enqueue(context: Context, request: BatchEnqueueRequest): RPCResult<boolean> {
    if (this.taskQueue.size(context) + request.list.length > MAX_BUFFERED_TASKS) {
      return { output: false };
    }
    for (const uuid of request.list) {
      this.taskQueue.publish(context, uuid);
    }
    return { output: true };
  }

  @rpc({ inputCodec: stringCodec })
  public completeChildWorkflow(context: Context, childWorkflowId: string): void {
    this.childComplete.publish(context, childWorkflowId, undefined);
  }
}

export const parentFlow = new ParentFlow();
