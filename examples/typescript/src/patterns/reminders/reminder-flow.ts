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
  Attribute,
  Channel,
  StepList,
  StepMovement,
  Timer,
  Wait,
  forceComplete,
  goTo,
  goToMulti,
  rpc,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { DAY_MS } from "../../config/env.js";
import {
  serviceDependency,
  type ServiceDependency,
} from "../shared/service-dependency.js";

export const DA_STATUS = "Status";
export const SIGNAL_NAME_OPT_OUT_REMINDER = "OptOutReminder";
export const INTERNAL_CHANNEL_COMPLETE_PROCESS = "CompleteProcess";

const REMINDER_WAIT_MS = 5_000;

class Init implements Step<void> {
  public constructor(private readonly flow: ReminderFlow) {}

  public getStepType(): string {
    return "Init";
  }

  public execute(context: Context, _input: void): StepDecision {
    this.flow.status.set(context, "INITIATED");
    return goToMulti(
      StepMovement.of(this.flow.processTimeoutStep, undefined),
      StepMovement.of(this.flow.reminderStep, undefined),
    );
  }
}

class ProcessTimeout implements Step<void> {
  public constructor(
    private readonly flow: ReminderFlow,
    private readonly service: ServiceDependency,
  ) {}

  public getStepType(): string {
    return "ProcessTimeout";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(
      Timer.byDuration(60 * DAY_MS),
      this.flow.completeProcess.forOne(),
    );
  }

  public execute(context: Context, _input: void): StepDecision {
    const currentStatus = this.flow.status.get(context);
    const resultStatus = currentStatus === "ACCEPTED" ? "ACCEPTED" : "TIMEOUT";
    this.service.updateExternalSystem(`notify for status: ${resultStatus}`);
    return forceComplete("done");
  }
}

class Reminder implements Step<void> {
  public constructor(
    private readonly flow: ReminderFlow,
    private readonly service: ServiceDependency,
  ) {}

  public getStepType(): string {
    return "Reminder";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(
      Timer.byDuration(REMINDER_WAIT_MS),
      this.flow.optOutReminder.forOne(),
    );
  }

  public execute(context: Context, _input: void): StepDecision {
    const currentStatus = this.flow.status.get(context);
    if (currentStatus === "ACCEPTED") {
      console.log("Reminder state timer expired, but status already ACCEPTED");
      return forceComplete("done");
    }

    if (this.flow.optOutReminder.results(context).length > 0) {
      this.service.updateExternalSystem("user opted out - no more reminders");
      return forceComplete("done - opt out");
    }

    this.service.sendEmail("Reminder:xxx please respond", "Hello xxx, ...");
    return goTo(this.flow.reminderStep, undefined);
  }
}

export class ReminderFlow implements Flow<void> {
  public readonly status = new Attribute(DA_STATUS, stringCodec);
  public readonly optOutReminder = new Channel(SIGNAL_NAME_OPT_OUT_REMINDER, voidCodec);
  public readonly completeProcess = new Channel(INTERNAL_CHANNEL_COMPLETE_PROCESS, voidCodec);

  private readonly initStep: Init;
  private readonly processTimeout: ProcessTimeout;
  private readonly reminder: Reminder;

  public constructor(service: ServiceDependency = serviceDependency) {
    this.initStep = new Init(this);
    this.processTimeout = new ProcessTimeout(this, service);
    this.reminder = new Reminder(this, service);
  }

  public get processTimeoutStep(): Step<void> {
    return this.processTimeout;
  }

  public get reminderStep(): Step<void> {
    return this.reminder;
  }

  public getFlowType(): string {
    return "ReminderFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initStep).otherSteps(
      this.processTimeout,
      this.reminder,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.status],
      channels: [this.optOutReminder, this.completeProcess],
    };
  }

  @rpc()
  public accept(context: Context): void {
    const currentStatus = this.status.get(context);
    if (currentStatus !== "INITIATED") {
      throw new Error("can only accept in INITIATED status");
    }
    this.status.set(context, "ACCEPTED");
    this.completeProcess.publish(context, undefined);
  }
}

export const reminderFlow = new ReminderFlow();
