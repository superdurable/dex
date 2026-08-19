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
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

class RetryExecuteStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "RetryExecuteStep";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: {
        initialIntervalMs: 1000,
        backoffCoefficient: 2,
        maximumAttempts: 5,
      },
    };
  }

  public execute(context: Context, readyAfterAttempt: number): StepDecision {
    if (context.attempt < readyAfterAttempt) {
      throw new Error(`not ready on attempt ${context.attempt}`);
    }
    return gracefulComplete("ready");
  }
}

export class RetryFlow implements Flow<number> {
  private readonly start = new RetryExecuteStep();

  public getFlowType(): string {
    return "RetryFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const retryFlow = new RetryFlow();
