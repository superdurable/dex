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
  deadEnd,
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

class HeartbeatStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "HeartbeatStep";
  }

  public getStepOptions(): StepOptions {
    return {
      executeMethodTimeoutMs: 60_000,
      heartbeatTimeoutMs: 10_000,
      executeRetry: {
        maximumAttempts: 3,
      },
    };
  }

  public async execute(context: Context, batches: number): Promise<StepDecision> {
    for (let batch = 0; batch < batches; batch++) {
      if (context.cancellationSignal.aborted) {
        return deadEnd();
      }
      await new Promise((resolve) => setTimeout(resolve, 2_000));
    }
    return gracefulComplete("processed");
  }
}

export class HeartbeatFlow implements Flow<number> {
  private readonly start = new HeartbeatStep();

  public getFlowType(): string {
    return "HeartbeatFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const heartbeatFlow = new HeartbeatFlow();
