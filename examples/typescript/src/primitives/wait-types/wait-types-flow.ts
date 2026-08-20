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
  ConditionCombination,
  StepList,
  Timer,
  Wait,
  gracefulComplete,
  jsonCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export interface WaitTypesInput {
  mode: string;
  timeoutSeconds: number;
}

const waitTypesInputCodec = jsonCodec<WaitTypesInput>();

class WaitTypesStep implements Step<WaitTypesInput> {
  public readonly inputCodec = waitTypesInputCodec;

  public constructor(private readonly flow: WaitTypesFlow) {}

  public getStepType(): string {
    return "WaitTypesStep";
  }

  public waitFor(_context: Context, input: WaitTypesInput): Wait {
    const timeoutMs = input.timeoutSeconds * 1000;
    if (input.mode === "any") {
      return Wait.anyOf(
        this.flow.channelA.forOne("signal"),
        Timer.byDuration(timeoutMs, "timeout"),
      );
    }
    if (input.mode === "all") {
      return Wait.allOf(
        this.flow.channelA.forOne("signal-a"),
        this.flow.channelB.forOne("signal-b"),
      );
    }
    if (input.mode === "combo") {
      return Wait.anyCombinationOf(
        ConditionCombination.of(
          this.flow.channelA.forOne("signal-a"),
          Timer.byDuration(timeoutMs, "timeout"),
        ),
        ConditionCombination.of(this.flow.channelB.forOne("signal-b")),
      );
    }
    throw new Error(`unknown wait mode ${input.mode}`);
  }

  public execute(_context: Context, input: WaitTypesInput): StepDecision {
    return gracefulComplete(input.mode);
  }
}

export class WaitTypesFlow implements Flow<WaitTypesInput> {
  public readonly channelA = new Channel("SignalA", stringCodec);
  public readonly channelB = new Channel("SignalB", stringCodec);
  private readonly waitTypes = new WaitTypesStep(this);

  public getFlowType(): string {
    return "WaitTypesFlow";
  }

  public getSteps() {
    return StepList.startStep(this.waitTypes);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.channelA, this.channelB] };
  }

  @rpc()
  public signalA(context: Context): void {
    this.channelA.publish(context, "signal-a");
  }

  @rpc()
  public signalB(context: Context): void {
    this.channelB.publish(context, "signal-b");
  }
}

export const waitTypesFlow = new WaitTypesFlow();
