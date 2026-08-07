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
  IndexType,
  StepList,
  StepMovement,
  Timer,
  Wait,
  deadEnd,
  goTo,
  goToMulti,
  gracefulComplete,
  int64Codec,
  optionalCodec,
  rpc,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { DAY_MS } from "../../config/env.js";
import {
  myDependencyService,
  type MyDependencyService,
} from "../my-dependency-service.js";
import {
  Status,
  engagementDescriptionCodec,
  engagementInputCodec,
  statusCodec,
  type EngagementDescription,
  type EngagementInput,
} from "./models.js";

export const STATUS_SEARCH_KEY = "CustomKeywordField";

const PROCESS_TIMEOUT_MS = 60 * DAY_MS;
const REMINDER_MS = 5_000;

export class EngagementFlow implements Flow<EngagementInput> {
  public readonly employerId = new Attribute("EmployerId", stringCodec);
  public readonly jobSeekerId = new Attribute("JobSeekerId", stringCodec);
  public readonly engagementStatus = new Attribute("EngagementStatus", statusCodec, {
    type: IndexType.KEYWORD,
    indexKey: STATUS_SEARCH_KEY,
  });
  public readonly lastUpdateTimestamp = new Attribute("LastUpdateTimeMillis", int64Codec);
  public readonly notes = new Attribute("notes", optionalCodec(stringCodec));
  public readonly optOutReminder = new Channel("OptOutReminder", voidCodec);
  public readonly completeProcess = new Channel("CompleteProcess", voidCodec);

  public readonly initialize = new Initialize(this);
  public readonly processTimeout = new ProcessTimeout(this);
  public readonly reminder = new Reminder(this);
  public readonly notifyExternalSystem = new NotifyExternalSystem(this);

  public constructor(public readonly service: MyDependencyService = myDependencyService) {}

  public getFlowType(): string {
    return "EngagementFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initialize).otherSteps(
      this.processTimeout,
      this.reminder,
      this.notifyExternalSystem,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [
        this.employerId,
        this.jobSeekerId,
        this.engagementStatus,
        this.lastUpdateTimestamp,
        this.notes,
      ],
      channels: [this.optOutReminder, this.completeProcess],
    };
  }

  @rpc({ outputCodec: engagementDescriptionCodec })
  public describe(context: Context): RPCResult<EngagementDescription> {
    return { output: this.describeEngagement(context) };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: statusCodec })
  public decline(context: Context, note: string): RPCResult<Status> {
    const status = this.engagementStatus.get(context);
    if (status !== Status.INITIATED) {
      throw new Error(
        `can only decline an initiated engagement; current status is "${status}"`,
      );
    }
    this.updateStatus(context, Status.DECLINED, note);
    return {
      output: Status.DECLINED,
      nextSteps: [StepMovement.of(this.notifyExternalSystem, Status.DECLINED)],
    };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: statusCodec })
  public accept(context: Context, note: string): RPCResult<Status> {
    const status = this.engagementStatus.get(context);
    if (status !== Status.INITIATED && status !== Status.DECLINED) {
      throw new Error(
        `can only accept an initiated or declined engagement; current status is "${status}"`,
      );
    }
    this.updateStatus(context, Status.ACCEPTED, note);
    this.completeProcess.publish(context, undefined);
    return {
      output: Status.ACCEPTED,
      nextSteps: [StepMovement.of(this.notifyExternalSystem, Status.ACCEPTED)],
    };
  }

  public describeEngagement(context: Context): EngagementDescription {
    return {
      employerId: this.employerId.get(context),
      jobSeekerId: this.jobSeekerId.get(context),
      notes: this.notes.get(context) ?? "",
      currentStatus: this.engagementStatus.get(context),
    };
  }

  public updateStatus(context: Context, status: Status, note: string): void {
    this.engagementStatus.set(context, status);
    this.lastUpdateTimestamp.set(context, BigInt(Date.now()));
    let currentNotes = this.notes.get(context) ?? "";
    if (note.length > 0) {
      currentNotes += `;${note}`;
    }
    this.notes.set(context, currentNotes);
  }
}

class Initialize implements Step<EngagementInput> {
  public readonly inputCodec = engagementInputCodec;

  public constructor(private readonly flow: EngagementFlow) {}

  public getStepType(): string {
    return "Initialize";
  }

  public execute(context: Context, input: EngagementInput): StepDecision {
    this.flow.employerId.set(context, input.employerId);
    this.flow.jobSeekerId.set(context, input.jobSeekerId);
    this.flow.engagementStatus.set(context, Status.INITIATED);
    this.flow.lastUpdateTimestamp.set(context, BigInt(Date.now()));
    this.flow.notes.set(context, input.notes);
    return goToMulti(
      StepMovement.of(this.flow.processTimeout, undefined),
      StepMovement.of(this.flow.reminder, undefined),
      StepMovement.of(this.flow.notifyExternalSystem, Status.INITIATED),
    );
  }
}

class ProcessTimeout implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: EngagementFlow) {}

  public getStepType(): string {
    return "ProcessTimeout";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(
      Timer.byDuration(PROCESS_TIMEOUT_MS),
      this.flow.completeProcess.forOne(),
    );
  }

  public execute(context: Context, _input: void): StepDecision {
    const description = this.flow.describeEngagement(context);
    let result = "timeout";
    if (description.currentStatus === Status.ACCEPTED) {
      result = "done";
    }
    this.flow.service.updateExternalSystem(
      `engagement from employer ${description.employerId} to job seeker ${description.jobSeekerId} finished with status ${description.currentStatus}`,
    );
    return gracefulComplete(result);
  }
}

class Reminder implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: EngagementFlow) {}

  public getStepType(): string {
    return "Reminder";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(Timer.byDuration(REMINDER_MS), this.flow.optOutReminder.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const status = this.flow.engagementStatus.get(context);
    if (status !== Status.INITIATED) {
      return deadEnd();
    }
    if (this.flow.optOutReminder.results(context).length > 0) {
      this.flow.updateStatus(context, status, "user opted out of reminders");
      return deadEnd();
    }
    const seekerId = this.flow.jobSeekerId.get(context);
    this.flow.service.sendEmail(
      seekerId,
      "Reminder: please respond",
      "Please respond to the engagement.",
    );
    return goTo(this.flow.reminder, undefined);
  }
}

class NotifyExternalSystem implements Step<Status> {
  public readonly inputCodec = statusCodec;

  public constructor(private readonly flow: EngagementFlow) {}

  public getStepType(): string {
    return "NotifyExternalSystem";
  }

  public execute(context: Context, status: Status): StepDecision {
    this.flow.service.updateExternalSystem(
      `notify engagement from employer ${this.flow.employerId.get(context)} to job seeker ${this.flow.jobSeekerId.get(context)} for status ${status}`,
    );
    return deadEnd();
  }
}

export const engagementFlow = new EngagementFlow();
