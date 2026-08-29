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
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export const TRACKER_DURATION_MS = 5 * 60 * 1_000;

const activeChannel = new Channel("Active", voidCodec);

class ProcessInactivenessStep implements Step<void> {
  public getStepType(): string {
    return "ProcessInactivenessStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    console.log("No activity arrived before the timer fired");
    return gracefulComplete();
  }
}

class TrackerStep implements Step<void> {
  public getStepType(): string {
    return "TrackerStep";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(
      Timer.byDuration(TRACKER_DURATION_MS),
      activeChannel.forOne(),
    );
  }

  public execute(context: Context, _input: void): StepDecision {
    if (context.hasTimerFired()) {
      return goTo(ProcessInactivenessStep, undefined);
    }
    return goTo(TrackerStep, undefined);
  }
}

export class InactivenessTrackerFlow implements Flow<void> {
  private readonly trackerStep = new TrackerStep();
  private readonly processInactivenessStep = new ProcessInactivenessStep();

  public getFlowType(): string {
    return "InactivenessTrackerFlow";
  }

  public getSteps() {
    return StepList.startStep(this.trackerStep).otherSteps(
      this.processInactivenessStep,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [activeChannel] };
  }

  @rpc()
  public recordActivity(context: Context): void {
    activeChannel.publish(context, undefined);
  }
}

export const inactivenessTrackerFlow = new InactivenessTrackerFlow();
