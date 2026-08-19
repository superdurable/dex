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
  StepList,
  Timer,
  Wait,
  goTo,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export const RESET_TIMER_CHANNEL = "RESET_TIMER_CHANNEL";
export const TIMER_DURATION_MS = 5 * 60 * 1000;

class ResettableTimerStep implements Step<void> {
  public constructor(private readonly flow: ResettableTimerFlow) {}

  public getStepType(): string {
    return "ResettableTimerStep";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(
      Timer.byDuration(TIMER_DURATION_MS),
      this.flow.resetTimerChannel.forOne(),
    );
  }

  public execute(context: Context, _input: void): StepDecision {
    if (context.hasTimerFired()) {
      return goTo(this.flow.timerExpiredStep, undefined);
    }
    return goTo(this.flow.resettableTimerStep, undefined);
  }
}

class TimerExpired implements Step<void> {
  public getStepType(): string {
    return "TimerExpired";
  }

  public execute(_context: Context, _input: void): StepDecision {
    console.log("Timer fired; this is where we would send an email");
    return gracefulComplete();
  }
}

export class ResettableTimerFlow implements Flow<void> {
  public readonly resetTimerChannel = new Channel(RESET_TIMER_CHANNEL, stringCodec);

  private readonly resettableTimer = new ResettableTimerStep(this);
  private readonly timerExpired = new TimerExpired();

  public get resettableTimerStep(): Step<void> {
    return this.resettableTimer;
  }

  public get timerExpiredStep(): Step<void> {
    return this.timerExpired;
  }

  public getFlowType(): string {
    return "ResettableTimerFlow";
  }

  public getSteps() {
    return StepList.startStep(this.resettableTimer).otherSteps(this.timerExpired);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.resetTimerChannel] };
  }

  @rpc()
  public sendResetMessage(context: Context): void {
    this.resetTimerChannel.publish(context, "reset");
  }
}

export const resettableTimerFlow = new ResettableTimerFlow();
