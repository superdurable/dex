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
  Timer,
  Wait,
  goTo,
  gracefulComplete,
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

import {
  myDependencyService,
  type MyDependencyService,
} from "../../shared/my-dependency-service.js";
import { signupFormCodec, type SignupForm } from "./signup-form.js";

const VERIFY_TIMER_MS = 24_000;

const verifyEmail = new Channel("VerifyEmail", voidCodec);
const task1Completed = new Channel("Task1Completed", voidCodec);
const task2Completed = new Channel("Task2Completed", voidCodec);

export class UserOnboardingFlow implements Flow<SignupForm> {
  public readonly form = new Attribute("Form", signupFormCodec);
  public readonly status = new Attribute("Status", stringCodec);

  public readonly submit = new Submit(this);
  public readonly verifyEmailStep = new VerifyEmail(this);
  public readonly accomplishTask1Step = new AccomplishTask1(this);
  public readonly accomplishTask2Step = new AccomplishTask2(this);

  public constructor(public readonly service: MyDependencyService = myDependencyService) {}

  public getFlowType(): string {
    return "UserOnboardingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.submit).otherSteps(
      this.verifyEmailStep,
      this.accomplishTask1Step,
      this.accomplishTask2Step,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.form, this.status],
      channels: [verifyEmail, task1Completed, task2Completed],
    };
  }

  @rpc({ name: "verify", outputCodec: stringCodec })
  public verifySignup(context: Context): RPCResult<string> {
    if (this.status.get(context) !== "waiting_for_verification") {
      return { output: "already verified" };
    }
    verifyEmail.publish(context, undefined);
    return { output: "verified" };
  }

  @rpc({ name: "accomplishTask1", outputCodec: stringCodec })
  public accomplishTask1(context: Context): RPCResult<string> {
    if (this.status.get(context) !== "waiting_for_task_1") {
      return { output: "task 1 is not waiting" };
    }
    task1Completed.publish(context, undefined);
    return { output: "task 1 accomplished" };
  }

  @rpc({ name: "accomplishTask2", outputCodec: stringCodec })
  public accomplishTask2(context: Context): RPCResult<string> {
    if (this.status.get(context) !== "waiting_for_task_2") {
      return { output: "task 2 is not waiting" };
    }
    task2Completed.publish(context, undefined);
    return { output: "task 2 accomplished" };
  }
}

class Submit implements Step<SignupForm> {
  public constructor(private readonly flow: UserOnboardingFlow) {}

  public getStepType(): string {
    return "Submit";
  }

  public execute(context: Context, input: SignupForm): StepDecision {
    this.flow.form.set(context, input);
    this.flow.status.set(context, "waiting_for_verification");
    this.flow.service.sendEmail(input.email, "verify your email", "start your onboarding");
    return goTo(VerifyEmail, undefined);
  }
}

class VerifyEmail implements Step<void> {
  public constructor(private readonly flow: UserOnboardingFlow) {}

  public getStepType(): string {
    return "VerifyEmail";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(Timer.byDuration(VERIFY_TIMER_MS), verifyEmail.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const signupForm = this.flow.form.get(context);
    if (verifyEmail.results(context).length > 0) {
      this.flow.status.set(context, "waiting_for_task_1");
      this.flow.service.sendEmail(
        signupForm.email,
        "complete onboarding task 1",
        "task 1 is ready",
      );
      return goTo(AccomplishTask1, undefined);
    }
    this.flow.service.sendEmail(
      signupForm.email,
      "verification reminder",
      "please verify your email",
    );
    return goTo(VerifyEmail, undefined);
  }
}

class AccomplishTask1 implements Step<void> {
  public constructor(private readonly flow: UserOnboardingFlow) {}

  public getStepType(): string {
    return "AccomplishTask1";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(Timer.byDuration(VERIFY_TIMER_MS), task1Completed.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const signupForm = this.flow.form.get(context);
    if (task1Completed.results(context).length > 0) {
      this.flow.status.set(context, "waiting_for_task_2");
      this.flow.service.sendEmail(
        signupForm.email,
        "complete onboarding task 2",
        "task 2 is ready",
      );
      return goTo(AccomplishTask2, undefined);
    }
    this.flow.service.sendEmail(
      signupForm.email,
      "task 1 reminder",
      "please complete onboarding task 1",
    );
    return goTo(AccomplishTask1, undefined);
  }
}

class AccomplishTask2 implements Step<void> {
  public constructor(private readonly flow: UserOnboardingFlow) {}

  public getStepType(): string {
    return "AccomplishTask2";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(Timer.byDuration(VERIFY_TIMER_MS), task2Completed.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const signupForm = this.flow.form.get(context);
    if (task2Completed.results(context).length > 0) {
      this.flow.status.set(context, "completed");
      this.flow.service.sendEmail(
        signupForm.email,
        "onboarding complete",
        "welcome aboard",
      );
      return gracefulComplete("onboarding completed");
    }
    this.flow.service.sendEmail(
      signupForm.email,
      "task 2 reminder",
      "please complete onboarding task 2",
    );
    return goTo(AccomplishTask2, undefined);
  }
}

export const userOnboardingFlow = new UserOnboardingFlow();
