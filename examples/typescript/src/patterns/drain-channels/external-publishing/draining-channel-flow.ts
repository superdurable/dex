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
  forceCompleteIfChannelsEmpty,
  optionalCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export const QUEUE_CHANNEL = "queueChannel";

export const queueChannel = new Channel(QUEUE_CHANNEL, stringCodec);

const optionalInputCodec = optionalCodec(stringCodec);

class ProcessMessage implements Step<string | undefined> {
  public readonly inputCodec = optionalInputCodec;

  public getStepType(): string {
    return "ProcessMessage";
  }

  public waitFor(_context: Context, input: string | undefined): Wait {
    if (input === undefined) {
      return Wait.until(queueChannel.forOne());
    }
    return Wait.skipImmediately();
  }

  public async execute(context: Context, input: string | undefined): Promise<StepDecision> {
    if (input !== undefined) {
      console.log(`DrainingChannelFlow process message: ${input}`);
    } else {
      const values = queueChannel.results(context);
      if (values.length === 0) {
        throw new Error("No channel message found");
      }
      const value = values[0];
      if (value === undefined) {
        throw new Error("No channel message value found");
      }
      console.log(`DrainingChannelFlow process message: ${value}`);
    }

    // busy wait mirrors Java Thread.sleep inside the workflow step
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 200);
    });

    return forceCompleteIfChannelsEmpty(
      null,
      StepMovement.of(ProcessMessage, undefined),
      queueChannel,
    );
  }
}

export class DrainingChannelFlow implements Flow<string | undefined> {
  private readonly processMessage = new ProcessMessage();

  public getFlowType(): string {
    return "DrainingChannelFlow";
  }

  public getSteps() {
    return StepList.startStep(this.processMessage);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [queueChannel] };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public exampleRPC(context: Context, input: string): RPCResult<string> {
    queueChannel.publish(context, input);
    return { output: input };
  }
}

export const drainingChannelFlow = new DrainingChannelFlow();
