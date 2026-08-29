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
  booleanCodec,
  forceComplete,
  forceFail,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

class LongWaitStep implements Step<boolean> {
  public readonly inputCodec = booleanCodec;

  public getStepType(): string {
    return "LongWaitStep";
  }

  public waitFor(_context: Context, workflowSuccessful: boolean): Wait {
    if (workflowSuccessful) {
      return Wait.skipImmediately();
    }
    return Wait.until(Timer.byDuration(65_000));
  }

  public execute(_context: Context, _workflowSuccessful: boolean): StepDecision {
    return forceComplete("Workflow completed successfully");
  }
}

export class FlowGracefulTimeout implements Flow<boolean> {
  private readonly longWaitStep = new LongWaitStep();

  public getFlowType(): string {
    return "FlowGracefulTimeout";
  }

  public getSteps() {
    return StepList.startStep(this.longWaitStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }

  public handleTimeout(_context: Context): StepDecision {
    return forceFail("Workflow did not finish the task in time");
  }
}

export const flowGracefulTimeout = new FlowGracefulTimeout();
