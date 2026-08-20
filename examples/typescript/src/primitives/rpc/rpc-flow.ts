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
  Wait,
  doubleCodec,
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

const internal = new Channel("rpc-internal", voidCodec);

class RpcWait implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly flow: RpcFlow) {}

  public getStepType(): string {
    return "RpcWait";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.until(internal.forOne());
  }

  public execute(_context: Context, _input: number): StepDecision {
    return goTo(this.flow.completeStep, 0);
  }
}

class RpcComplete implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "RpcComplete";
  }

  public execute(_context: Context, input: number): StepDecision {
    return gracefulComplete(input + 1);
  }
}

export class RpcFlow implements Flow<number> {
  public readonly data = new Attribute("rpc-data", stringCodec);
  private readonly wait = new RpcWait(this);
  private readonly complete = new RpcComplete();

  public get completeStep(): Step<number> {
    return this.complete;
  }

  public getFlowType(): string {
    return "RpcFlow";
  }

  public getSteps() {
    return StepList.startStep(this.wait).otherSteps(this.complete);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.data], channels: [internal] };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public trigger(context: Context, input: string): RPCResult<string> {
    this.data.set(context, input);
    internal.publish(context, undefined);
    return { output: input };
  }
}

export const rpcFlow = new RpcFlow();
