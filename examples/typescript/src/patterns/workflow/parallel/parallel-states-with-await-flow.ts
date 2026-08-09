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
  StepMovement,
  Wait,
  deadEnd,
  doubleCodec,
  goToMulti,
  gracefulComplete,
  jsonCodec,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { jobSeekerCodec, type JobSeeker } from "./job-seeker.js";

export const NOTIFY_CHANNEL = "test_notify_channel";

const jobSeekerInputCodec = jsonCodec<JobSeeker>(jobSeekerCodec);
const countInputCodec = doubleCodec;

class Starting implements Step<number> {
  public readonly inputCodec = countInputCodec;

  public constructor(private readonly flow: ParallelStatesWithAwaitFlow) {}

  public getStepType(): string {
    return "Starting";
  }

  public execute(_context: Context, countOfJobSeekers: number): StepDecision {
    const movements: StepMovement<unknown>[] = [
      StepMovement.of(this.flow.awaitAllUsersNotifiedStep, countOfJobSeekers),
    ];
    for (let index = 1; index <= countOfJobSeekers; index += 1) {
      movements.push(
        StepMovement.of(this.flow.notifyUserStep, {
          id: String(index),
          email: "jobseeker@indeed.com",
          phoneNumber: "0987654321",
        }),
      );
    }
    return goToMulti(...movements);
  }
}

class NotifyUser implements Step<JobSeeker> {
  public readonly inputCodec = jobSeekerInputCodec;

  public constructor(private readonly flow: ParallelStatesWithAwaitFlow) {}

  public getStepType(): string {
    return "NotifyUser";
  }

  public execute(context: Context, jobSeeker: JobSeeker): StepDecision {
    const sleepMs = Math.floor(Math.random() * 5000);
    const sleepUntil = Date.now() + sleepMs;
    while (Date.now() < sleepUntil) {
      // simulate variable notification latency
    }

    const message = `[FAKE] Notifying user of something: ${jobSeeker.id}`;
    console.log(message);
    context.recordEvent("notification", message, stringCodec);
    this.flow.notifyChannel.publish(context, "I sent something");
    return deadEnd();
  }
}

class AwaitAllUsersNotified implements Step<number> {
  public readonly inputCodec = countInputCodec;

  public constructor(private readonly flow: ParallelStatesWithAwaitFlow) {}

  public getStepType(): string {
    return "AwaitAllUsersNotified";
  }

  public waitFor(_context: Context, countOfJobSeekers: number): Wait {
    return Wait.until(this.flow.notifyChannel.forN(countOfJobSeekers));
  }

  public execute(context: Context, countOfJobSeekers: number): StepDecision {
    const message = `[FAKE] Sent all ${countOfJobSeekers} notifications`;
    context.recordEvent("sent-notifications", message, stringCodec);
    return gracefulComplete();
  }
}

export class ParallelStatesWithAwaitFlow implements Flow<number> {
  public readonly notifyChannel = new Channel(NOTIFY_CHANNEL, stringCodec);

  private readonly starting = new Starting(this);
  private readonly notifyUser = new NotifyUser(this);
  private readonly awaitAllUsersNotified = new AwaitAllUsersNotified(this);

  public get notifyUserStep(): Step<JobSeeker> {
    return this.notifyUser;
  }

  public get awaitAllUsersNotifiedStep(): Step<number> {
    return this.awaitAllUsersNotified;
  }

  public getFlowType(): string {
    return "ParallelStatesWithAwaitFlow";
  }

  public getSteps() {
    return StepList.startStep(this.starting).otherSteps(
      this.notifyUser,
      this.awaitAllUsersNotified,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.notifyChannel] };
  }
}

export const parallelStatesWithAwaitFlow = new ParallelStatesWithAwaitFlow();
