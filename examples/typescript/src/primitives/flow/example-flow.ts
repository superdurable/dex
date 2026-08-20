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

class FinishStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public execute(context: Context, input: number): StepDecision {
    status.set(context, "done");
    return gracefulComplete(input + 1);
  }
}

class ExampleStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly finish: FinishStep) {}

  public waitFor(context: Context, _input: number): Wait {
    status.set(context, "running");
    return Wait.skipImmediately();
  }

  public execute(_context: Context, input: number): StepDecision {
    return goTo(this.finish, input + 1);
  }
}

const status = new Attribute("status", stringCodec);
const notify = new Channel("notify", voidCodec);

export class ExampleFlow implements Flow<number> {
  private readonly finish = new FinishStep();
  private readonly example = new ExampleStep(this.finish);

  public getFlowType(): string {
    return "ExampleFlow";
  }

  public getSteps() {
    return StepList.startStep(this.example).otherSteps(this.finish);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [status], channels: [notify] };
  }

  @rpc({ outputCodec: stringCodec })
  public describe(context: Context): RPCResult<string> {
    return { output: status.get(context) ?? "" };
  }
}

export const exampleFlow = new ExampleFlow();
