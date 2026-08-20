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
  retryAfter,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

class CustomRetryStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "CustomRetryStep";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: {
        maximumAttempts: 5,
      },
    };
  }

  public execute(context: Context, readyAfterAttempt: number): StepDecision {
    if (context.attempt < readyAfterAttempt) {
      const cause = new Error(`not ready on attempt ${context.attempt}`);
      throw retryAfter(7, cause);
    }
    return gracefulComplete("ready");
  }
}

export class CustomRetryFlow implements Flow<number> {
  private readonly start = new CustomRetryStep();

  public getFlowType(): string {
    return "CustomRetryFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const customRetryFlow = new CustomRetryFlow();
