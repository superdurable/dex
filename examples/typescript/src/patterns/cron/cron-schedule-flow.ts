/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
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
  StepMovement,
  Timer,
  Wait,
  deadEnd,
  forceFail,
  goTo,
  goToMulti,
  gracefulComplete,
  jsonCodec,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export type IntervalUnit = "minute" | "hour" | "day";

export interface Interval {
  value: number;
  unit: IntervalUnit;
}

export interface CronScheduleInput {
  interval: Interval;
  runCount: number;
}

interface ScheduleState {
  interval: Interval;
  remainingRuns: number;
}

interface RunInput {
  runNumber: number;
  isFinal: boolean;
}

const cronScheduleInputCodec = jsonCodec<CronScheduleInput>();
const scheduleStateCodec = jsonCodec<ScheduleState>();
const runInputCodec = jsonCodec<RunInput>();

const intervalMs = (interval: Interval): number => {
  switch (interval.unit) {
    case "minute":
      return interval.value * 60_000;
    case "hour":
      return interval.value * 3_600_000;
    case "day":
      return interval.value * 86_400_000;
  }
};

class Start implements Step<CronScheduleInput> {
  public readonly inputCodec = cronScheduleInputCodec;

  public constructor(private readonly schedule: WaitForSchedule) {}

  public getStepType(): string {
    return "CronScheduleStart";
  }

  public execute(_context: Context, input: CronScheduleInput): StepDecision {
    if (input.runCount <= 0 || input.interval.value <= 0) {
      return forceFail("interval value and run count must be positive");
    }
    return goTo(WaitForSchedule, {
      interval: input.interval,
      remainingRuns: input.runCount,
    });
  }
}

class WaitForSchedule implements Step<ScheduleState> {
  public readonly inputCodec = scheduleStateCodec;

  public constructor(
    private readonly trigger: Channel<void>,
    private readonly skip: Channel<void>,
    private readonly run: Run,
  ) {}

  public getStepType(): string {
    return "CronScheduleWait";
  }

  public waitFor(_context: Context, state: ScheduleState): Wait {
    return Wait.anyOf(
      Timer.byDuration(intervalMs(state.interval)),
      this.trigger.forOne(),
      this.skip.forOne(),
    );
  }

  public execute(context: Context, state: ScheduleState): StepDecision {
    if (this.skip.results(context).length > 0) {
      return this.nextSchedule(state);
    }
    const input: RunInput = {
      runNumber: state.remainingRuns,
      isFinal: state.remainingRuns === 1,
    };
    if (input.isFinal) {
      return goTo(Run, input);
    }
    return goToMulti(
      StepMovement.of(Run, input),
      StepMovement.of(WaitForSchedule, {
        interval: state.interval,
        remainingRuns: state.remainingRuns - 1,
      }),
    );
  }

  private nextSchedule(state: ScheduleState): StepDecision {
    if (state.remainingRuns === 1) {
      return gracefulComplete();
    }
    return goTo(WaitForSchedule, {
      interval: state.interval,
      remainingRuns: state.remainingRuns - 1,
    });
  }

}

class Run implements Step<RunInput> {
  public readonly inputCodec = runInputCodec;

  public getStepType(): string {
    return "CronScheduleRun";
  }

  public execute(context: Context, input: RunInput): StepDecision {
    context.recordEvent("cron-schedule-run", `run-${input.runNumber}`, stringCodec);
    return input.isFinal ? gracefulComplete() : deadEnd();
  }
}

export class CronScheduleFlow implements Flow<CronScheduleInput> {
  public readonly trigger = new Channel("cron-schedule-trigger", voidCodec);
  public readonly skip = new Channel("cron-schedule-skip", voidCodec);
  private readonly run = new Run();
  private readonly schedule = new WaitForSchedule(this.trigger, this.skip, this.run);
  private readonly start = new Start(this.schedule);

  public getFlowType(): string {
    return "CronScheduleFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.schedule, this.run);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.trigger, this.skip] };
  }
}

export const cronScheduleFlow = new CronScheduleFlow();
