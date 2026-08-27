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
  StepMovement,
  goToMulti,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { type JobSeeker } from "./job-seeker.js";

const stringInputCodec = stringCodec;

class Init implements Step<JobSeeker> {
  public constructor(private readonly flow: SimpleParallelStatesFlow) {}

  public getStepType(): string {
    return "Init";
  }

  public execute(_context: Context, input: JobSeeker): StepDecision {
    return goToMulti(
      StepMovement.of(SendTextMessage, input.phoneNumber),
      StepMovement.of(SendEmail, input.email),
    );
  }
}

class SendTextMessage implements Step<string> {
  public readonly inputCodec = stringInputCodec;

  public getStepType(): string {
    return "SendTextMessage";
  }

  public execute(context: Context, input: string): StepDecision {
    const message = `[FAKE] Sending text message to: ${input}`;
    console.log(message);
    context.recordEvent("text-message", message, stringCodec);
    return gracefulComplete();
  }
}

class SendEmail implements Step<string> {
  public readonly inputCodec = stringInputCodec;

  public getStepType(): string {
    return "SendEmail";
  }

  public execute(context: Context, input: string): StepDecision {
    const message = `[FAKE] Sending email to: ${input}`;
    console.log(message);
    context.recordEvent("email-notification", message, stringCodec);
    return gracefulComplete();
  }
}

export class SimpleParallelStatesFlow implements Flow<JobSeeker> {
  private readonly initStep = new Init(this);
  private readonly sendTextMessage = new SendTextMessage();
  private readonly sendEmail = new SendEmail();

  public get sendTextMessageStep(): Step<string> {
    return this.sendTextMessage;
  }

  public get sendEmailStep(): Step<string> {
    return this.sendEmail;
  }

  public getFlowType(): string {
    return "SimpleParallelStatesFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initStep).otherSteps(
      this.sendTextMessage,
      this.sendEmail,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const simpleParallelStatesFlow = new SimpleParallelStatesFlow();
