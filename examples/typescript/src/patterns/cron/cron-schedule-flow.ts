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
  gracefulComplete,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

class CronScheduleStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "CronScheduleStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete();
  }
}

export class CronScheduleFlow implements Flow<void> {
  private readonly cronScheduleStep = new CronScheduleStep();

  public getFlowType(): string {
    return "CronScheduleFlow";
  }

  public getSteps() {
    return StepList.startStep(this.cronScheduleStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const cronScheduleFlow = new CronScheduleFlow();
