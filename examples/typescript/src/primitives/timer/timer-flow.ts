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
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

class TimerStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "TimerStep";
  }

  public waitFor(_context: Context, input: number): Wait {
    return Wait.until(Timer.byDuration(input * 1000));
  }

  public execute(_context: Context, _input: number): StepDecision {
    return gracefulComplete("timer-fired");
  }
}

export class TimerFlow implements Flow<number> {
  private readonly start = new TimerStep();

  public getFlowType(): string {
    return "TimerFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const timerFlow = new TimerFlow();
