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
  FlowTimeoutPolicy,
  StepList,
  SubFlow,
  Wait,
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { HOUR_MS } from "../../config/env.js";

class SubFlowChildStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "SubFlowChildStep";
  }

  public execute(_context: Context, input: number): StepDecision {
    return gracefulComplete(input + 1);
  }
}

export class SubFlowChildFlow implements Flow<number> {
  private readonly start = new SubFlowChildStep();

  public getFlowType(): string {
    return "SubFlowChildFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

class SubFlowParentStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly target: SubFlowChildFlow) {}

  public getStepType(): string {
    return "SubFlowParentStep";
  }

  public waitFor(_context: Context, input: number): Wait {
    return Wait.until(
      SubFlow.run(this.target, input, {
        timeoutMs: HOUR_MS,
        timeoutPolicy: FlowTimeoutPolicy.CANCEL,
      }),
    );
  }

  public execute(context: Context, _input: number): StepDecision {
    const result = SubFlow.getConditionResults(context);
    const output = result.singleOutput(doubleCodec);
    const flowId = SubFlow.getFlowId(context);
    return gracefulComplete(`${flowId}|${output}`);
  }
}

export class SubFlowParentFlow implements Flow<number> {
  private readonly start: SubFlowParentStep;

  public constructor(private readonly target: SubFlowChildFlow) {
    this.start = new SubFlowParentStep(target);
  }

  public getFlowType(): string {
    return "SubFlowParentFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const subFlowChildFlow = new SubFlowChildFlow();
export const subFlowParentFlow = new SubFlowParentFlow(subFlowChildFlow);
