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
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export const QUEUE_SIGNAL_CHANNEL = "queueSignalChannel";

export const queueSignalChannel = new Channel(QUEUE_SIGNAL_CHANNEL, stringCodec);

const signalInputCodec = optionalCodec(stringCodec);

class ProcessSignal implements Step<string | undefined> {
  public readonly inputCodec = signalInputCodec;

  public getStepType(): string {
    return "ProcessSignal";
  }

  public waitFor(_context: Context, input: string | undefined): Wait {
    if (input === undefined) {
      return Wait.until(queueSignalChannel.forOne());
    }
    return Wait.skipImmediately();
  }

  public async execute(context: Context, input: string | undefined): Promise<StepDecision> {
    if (input !== undefined) {
      console.log(`DrainSignalChannelsFlow process signal value: ${input}`);
    } else {
      const values = queueSignalChannel.results(context);
      if (values.length === 0) {
        throw new Error("No signal request found");
      }
      const value = values[0];
      if (value === undefined) {
        throw new Error("No signal value found");
      }
      console.log(`DrainSignalChannelsFlow process signal value: ${value}`);
    }

    // busy wait mirrors Java Thread.sleep inside the workflow step
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 200);
    });

    return forceCompleteIfChannelsEmpty(
      null,
      StepMovement.of(ProcessSignal, undefined),
      queueSignalChannel,
    );
  }
}

export class DrainSignalChannelsFlow implements Flow<string | undefined> {
  private readonly processSignal = new ProcessSignal();

  public getFlowType(): string {
    return "DrainSignalChannelsFlow";
  }

  public getSteps() {
    return StepList.startStep(this.processSignal);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [queueSignalChannel] };
  }
}

export const drainSignalChannelsFlow = new DrainSignalChannelsFlow();
