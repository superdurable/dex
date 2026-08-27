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
  Channel,
  ExecuteFailure,
  StepList,
  Wait,
  booleanCodec,
  forceFail,
  goTo,
  gracefulComplete,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export const RETRY_CHANNEL = "manual-recovery-retry";
export const SKIP_CHANNEL = "manual-recovery-skip";

const retryChannel = new Channel(RETRY_CHANNEL, voidCodec);
const skipChannel = new Channel(SKIP_CHANNEL, voidCodec);

class DoWorkStep implements Step<boolean> {
  public readonly inputCodec = booleanCodec;

  public getStepType(): string {
    return "DoWorkStep";
  }

  public getStepOptions() {
    return {
      executeRetry: {
        initialIntervalMs: 1_000,
        backoffCoefficient: 2,
        maximumIntervalMs: 4_000,
        maximumAttempts: 4,
      },
      executeFailure: ExecuteFailure.proceedTo(ManualStep),
    };
  }

  public execute(_context: Context, shouldFail: boolean): StepDecision {
    if (shouldFail) {
      throw new Error("work failed");
    }
    return gracefulComplete("work completed");
  }
}

class ManualStep implements Step<boolean> {
  public readonly inputCodec = booleanCodec;

  public getStepType(): string {
    return "ManualStep";
  }

  public waitFor(_context: Context, _input: boolean): Wait {
    return Wait.anyOf(retryChannel.forOne(), skipChannel.forOne());
  }

  public execute(context: Context, _input: boolean): StepDecision {
    if (retryChannel.results(context).length > 0) {
      return goTo(DoWorkStep, false);
    }
    return forceFail("manual recovery skipped");
  }
}

export class ManualRecoveryFlow implements Flow<boolean> {
  private readonly doWorkStep = new DoWorkStep();
  private readonly manualStep = new ManualStep();

  public getFlowType(): string {
    return "ManualRecoveryFlow";
  }

  public getSteps() {
    return StepList.startStep(this.doWorkStep).otherSteps(this.manualStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      channels: [retryChannel, skipChannel],
    };
  }
}

export const manualRecoveryFlow = new ManualRecoveryFlow();
