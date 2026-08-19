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

export class UserSignupFlow implements Flow<SignupForm> {
  public readonly form = new Attribute("Form", signupFormCodec);
  public readonly status = new Attribute("Status", stringCodec);
  public readonly verify = new Channel("Verify", voidCodec);

  public readonly submit = new Submit(this);
  public readonly verifyStep = new Verify(this);

  public constructor(public readonly service: MyDependencyService = myDependencyService) {}

  public getFlowType(): string {
    return "UserSignupFlow";
  }

  public getSteps() {
    return StepList.startStep(this.submit).otherSteps(this.verifyStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.form, this.status],
      channels: [this.verify],
    };
  }

  @rpc({ name: "verify", outputCodec: stringCodec })
  public verifySignup(context: Context): RPCResult<string> {
    if (this.status.get(context) === "verified") {
      return { output: "already verified" };
    }
    this.status.set(context, "verified");
    this.verify.publish(context, undefined);
    return { output: "done" };
  }
}

class Submit implements Step<SignupForm> {
  public constructor(private readonly flow: UserSignupFlow) {}

  public getStepType(): string {
    return "Submit";
  }

  public execute(context: Context, input: SignupForm): StepDecision {
    this.flow.form.set(context, input);
    this.flow.status.set(context, "waiting");
    this.flow.service.sendEmail(input.email, "please verify the signup", "content");
    return goTo(this.flow.verifyStep, undefined);
  }
}

class Verify implements Step<void> {
  public constructor(private readonly flow: UserSignupFlow) {}

  public getStepType(): string {
    return "Verify";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(Timer.byDuration(VERIFY_TIMER_MS), this.flow.verify.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const signupForm = this.flow.form.get(context);
    if (this.flow.verify.results(context).length > 0) {
      this.flow.service.sendEmail(signupForm.email, "welcome", "welcome to Indeed!");
      return gracefulComplete("done");
    }
    this.flow.service.sendEmail(signupForm.email, "reminder", "please verify your email");
    return goTo(this.flow.verifyStep, undefined);
  }
}

export const userSignupFlow = new UserSignupFlow();
