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
  FlowNotActiveError,
  StepList,
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { getClient } from "../../../client-holder.js";
import { startOptions } from "../../../config/env.js";
import { EnqueueFailedException } from "./exceptions/enqueue-failed-exception.js";
import { NUM_PARENT_WORKFLOWS, parentFlow, type ParentFlow } from "./parent-flow.js";
import type { BatchEnqueueRequest } from "./models/batch-enqueue-request.js";

class Request implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly parent: ParentFlow) {}

  public getStepType(): string {
    return "Request";
  }

  public async execute(
    _context: Context,
    numberOfChildWfs: number,
  ): Promise<StepDecision> {
    const batch = this.generateTasks(numberOfChildWfs);
    const randSuffix = Math.floor(Math.random() * NUM_PARENT_WORKFLOWS) + 1;
    const parentWorkflowId = `parent_workflow_${randSuffix}`;
    const client = getClient();

    try {
      const success = await client.invokeRPC(
        this.parent.enqueue,
        parentWorkflowId,
        batch,
      );
      if (!success) {
        throw new EnqueueFailedException("Enqueue failed, retry in next attempt");
      }
    } catch (error) {
      if (error instanceof FlowNotActiveError) {
        await client.startFlow(this.parent, parentWorkflowId, batch, startOptions());
      } else {
        throw error;
      }
    }

    return gracefulComplete();
  }

  private generateTasks(numberOfChildWfs: number): BatchEnqueueRequest {
    const uuids: string[] = [];
    for (let index = 0; index < numberOfChildWfs; index += 1) {
      uuids.push(crypto.randomUUID());
    }
    return { list: uuids };
  }
}

export class RequestReceiverFlow implements Flow<number> {
  private readonly request = new Request(parentFlow);

  public getFlowType(): string {
    return "RequestReceiverFlow";
  }

  public getSteps() {
    return StepList.startStep(this.request);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const requestReceiverFlow = new RequestReceiverFlow();
