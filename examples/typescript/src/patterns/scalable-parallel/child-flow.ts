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
  StepList,
  Timer,
  Wait,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { getClient } from "../../client-holder.js";
import { isFlowMissingOrInactive } from "../../service-errors.js";

export const PARENT_WORKFLOW_ID = "ParentWorkflowId";

class Processing implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: ChildFlow) {}

  public getStepType(): string {
    return "Processing";
  }

  public waitFor(_context: Context, _input: string): Wait {
    const randomSeconds = Math.floor(Math.random() * 2);
    return Wait.until(Timer.byDuration(randomSeconds * 1000));
  }

  public async execute(context: Context, _input: string): Promise<StepDecision> {
    const parentId = this.flow.parentWorkflowId.get(context);
    if (parentId !== undefined) {
      // Lazy import avoids the parent↔child module cycle at load time.
      const { parentFlow } = await import("./parent-flow.js");
      try {
        await getClient().invokeRPC(
          parentFlow.completeChildWorkflow,
          parentId,
          context.flowId,
        );
      } catch (error) {
        if (isFlowMissingOrInactive(error)) {
          console.log(
            "Parent workflow may have completed, might be duplicate completion request, ignore it.",
          );
        } else {
          throw error;
        }
      }
    }
    return gracefulComplete();
  }
}

export class ChildFlow implements Flow<string> {
  public readonly parentWorkflowId = new Attribute(PARENT_WORKFLOW_ID, stringCodec);

  private readonly processing = new Processing(this);

  public getFlowType(): string {
    return "ChildFlow";
  }

  public getSteps() {
    return StepList.startStep(this.processing);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.parentWorkflowId] };
  }
}

export const childFlow = new ChildFlow();
