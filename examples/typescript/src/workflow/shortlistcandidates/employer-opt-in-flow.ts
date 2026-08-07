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
  IndexType,
  StepList,
  StepMovement,
  booleanCodec,
  deadEnd,
  forceComplete,
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

import { employerOptInInputCodec, type EmployerOptInInput } from "./employer-opt-in-input.js";

export class EmployerOptInFlow implements Flow<EmployerOptInInput> {
  public readonly employerId = new Attribute("EMPLOYER_OPT_IN_EmployerId", stringCodec, {
    type: IndexType.KEYWORD,
    indexKey: "CustomKeywordField",
  });
  public readonly optedIn = new Attribute("EMPLOYER_OPT_IN_Status", booleanCodec);

  public readonly optIn = new OptIn(this);
  public readonly optOutStep = new OptOut(this);

  public getFlowType(): string {
    return "EmployerOptInFlow";
  }

  public getSteps() {
    return StepList.startStep(this.optIn).otherSteps(this.optOutStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.employerId, this.optedIn],
    };
  }

  @rpc({ outputCodec: booleanCodec })
  public isOptedIn(context: Context): RPCResult<boolean> {
    return { output: this.optedIn.get(context) === true };
  }

  @rpc({ outputCodec: voidCodec })
  public optOut(_context: Context): RPCResult<void> {
    return { output: undefined, nextSteps: [StepMovement.of(this.optOutStep, undefined)] };
  }
}

class OptIn implements Step<EmployerOptInInput> {
  public readonly inputCodec = employerOptInInputCodec;

  public constructor(private readonly flow: EmployerOptInFlow) {}

  public getStepType(): string {
    return "OptIn";
  }

  public execute(context: Context, input: EmployerOptInInput): StepDecision {
    this.flow.employerId.set(context, input.employerId);
    this.flow.optedIn.set(context, true);
    return deadEnd();
  }
}

class OptOut implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: EmployerOptInFlow) {}

  public getStepType(): string {
    return "OptOut";
  }

  public execute(context: Context, _input: void): StepDecision {
    this.flow.optedIn.set(context, false);
    return forceComplete();
  }
}

export const employerOptInFlow = new EmployerOptInFlow();
