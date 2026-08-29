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
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import {
  serviceDependency,
  type ServiceDependency,
} from "../shared/service-dependency.js";

const REMINDER_WAIT_MS = 5_000;

export const optOut = new Channel("OptOut", voidCodec);

class ReminderStep implements Step<void> {
  public constructor(private readonly service: ServiceDependency) {}

  public getStepType(): string {
    return "ReminderStep";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(
      Timer.byDuration(REMINDER_WAIT_MS),
      optOut.forOne(),
    );
  }

  public execute(context: Context, _input: void): StepDecision {
    if (!context.hasTimerFired()) {
      return gracefulComplete();
    }
    this.service.sendEmail("Reminder: please respond", "Hello, ...");
    return goTo(ReminderStep, undefined);
  }
}

export class ReminderFlow implements Flow<void> {
  private readonly reminderStep: ReminderStep;

  public constructor(service: ServiceDependency = serviceDependency) {
    this.reminderStep = new ReminderStep(service);
  }

  public getFlowType(): string {
    return "ReminderFlow";
  }

  public getSteps() {
    return StepList.startStep(this.reminderStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [optOut] };
  }
}

export const reminderFlow = new ReminderFlow();
