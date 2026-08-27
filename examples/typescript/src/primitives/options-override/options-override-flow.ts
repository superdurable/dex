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
  Wait,
  goToMulti,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

class OverrideFirstStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: OptionsOverrideFlow) {}

  public getStepType(): string {
    return "OverrideFirstStep";
  }

  public execute(_context: Context, input: string): StepDecision {
    const override: StepOptions = {
      waitForRetry: { maximumAttempts: 2 },
      waitForFailure: "proceed",
    };
    const payload = `${input}_state1`;
    return goToMulti(StepMovement.of(OverrideSecondStep, payload, override));
  }
}

class OverrideSecondStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "OverrideSecondStep";
  }

  public waitFor(_context: Context, _input: string): Wait {
    throw new Error("state 2 wait failure");
  }

  public execute(context: Context, input: string): StepDecision {
    if (!context.waitForMethodFailed()) {
      throw new Error("waitFor failure was not reported");
    }
    return gracefulComplete(`${input}_state2`);
  }

  public getStepOptions(): StepOptions {
    return {
      waitForRetry: { maximumAttempts: 1 },
      waitForFailure: "failFlow",
    };
  }
}

export class OptionsOverrideFlow implements Flow<string> {
  private readonly second = new OverrideSecondStep();
  private readonly first = new OverrideFirstStep(this);

  public get secondStep(): Step<string> {
    return this.second;
  }

  public getFlowType(): string {
    return "OptionsOverrideFlow";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const optionsOverrideFlow = new OptionsOverrideFlow();
