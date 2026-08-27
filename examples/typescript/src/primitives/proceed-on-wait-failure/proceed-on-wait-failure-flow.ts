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
  Wait,
  goTo,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

class FinishStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "FinishStep";
  }

  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(input);
  }
}

class FailingWaitStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly finish: FinishStep) {}

  public getStepType(): string {
    return "FailingWaitStep";
  }

  public getStepOptions(): StepOptions {
    return {
      waitForRetry: { maximumAttempts: 2 },
      waitForFailure: "proceed",
    };
  }

  public waitFor(_context: Context, _input: string): Wait {
    throw new Error("planned WaitFor failure");
  }

  public execute(context: Context, input: string): StepDecision {
    if (!context.waitForMethodFailed()) {
      throw new Error("waitFor failure was not reported");
    }
    return goTo(FinishStep, `${input}_recovered`);
  }
}

export class ProceedOnWaitFailureFlow implements Flow<string> {
  private readonly finish = new FinishStep();
  private readonly failingWait = new FailingWaitStep(this.finish);

  public getFlowType(): string {
    return "ProceedOnWaitFailureFlow";
  }

  public getSteps() {
    return StepList.startStep(this.failingWait).otherSteps(this.finish);
  }
}

export const proceedOnWaitFailureFlow = new ProceedOnWaitFailureFlow();
