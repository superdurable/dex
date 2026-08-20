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
  deadEnd,
  goTo,
  goToMulti,
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

import { DAY_MS } from "../../config/env.js";
import {
  myDependencyService,
  type MyDependencyService,
} from "../../shared/my-dependency-service.js";

export const ready = new Channel("Ready", voidCodec);

export class OrchestrationFlow implements Flow<string> {
  public readonly data = new Attribute("data", stringCodec);

  public readonly callAPI1 = new CallAPI1(this);
  public readonly callAPI2 = new CallAPI2(this);
  public readonly callAPI3 = new CallAPI3(this);
  public readonly callAPI4 = new CallAPI4(this);

  public constructor(public readonly service: MyDependencyService = myDependencyService) {}

  public getFlowType(): string {
    return "OrchestrationFlow";
  }

  public getSteps() {
    return StepList.startStep(this.callAPI1).otherSteps(
      this.callAPI2,
      this.callAPI3,
      this.callAPI4,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.data],
      channels: [ready],
    };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public swap(context: Context, newData: string): RPCResult<string> {
    const oldData = this.data.get(context);
    this.data.set(context, newData);
    return { output: oldData };
  }
}

class CallAPI1 implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: OrchestrationFlow) {}

  public getStepType(): string {
    return "CallAPI1";
  }

  public execute(context: Context, input: string): StepDecision {
    this.flow.service.callAPI1(input);
    this.flow.data.set(context, input);
    return goToMulti(
      StepMovement.of(this.flow.callAPI2, undefined),
      StepMovement.of(this.flow.callAPI3, undefined),
    );
  }
}

class CallAPI2 implements Step<void> {
  public constructor(private readonly flow: OrchestrationFlow) {}

  public getStepType(): string {
    return "CallAPI2";
  }

  public execute(context: Context, _input: void): StepDecision {
    this.flow.service.callAPI2(this.flow.data.get(context));
    return deadEnd();
  }
}

class CallAPI3 implements Step<void> {
  public constructor(private readonly flow: OrchestrationFlow) {}

  public getStepType(): string {
    return "CallAPI3";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(Timer.byDuration(DAY_MS), ready.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const value = this.flow.data.get(context);
    this.flow.service.callAPI3(value);
    if (context.hasTimerFired()) {
      return goTo(this.flow.callAPI4, undefined);
    }
    return gracefulComplete(value);
  }
}

class CallAPI4 implements Step<void> {
  public constructor(private readonly flow: OrchestrationFlow) {}

  public getStepType(): string {
    return "CallAPI4";
  }

  public execute(context: Context, _input: void): StepDecision {
    const value = this.flow.data.get(context);
    this.flow.service.callAPI4(value);
    return gracefulComplete(value);
  }
}

export const orchestrationFlow = new OrchestrationFlow();
