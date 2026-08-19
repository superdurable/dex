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
  booleanCodec,
  doubleCodec,
  goTo,
  gracefulComplete,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export const INTERNAL_CHANNEL_COMMAND = "internal_channel_command";
export const SIGNAL_CHANNEL_COMMAND_RETRY = "signal_channel_command_retry";
export const SIGNAL_CHANNEL_COMMAND_SKIP = "signal_channel_command_skip";
export const NUMBER_OF_RETRIES = "number_of_retries";

class Init implements Step<void> {
  public constructor(private readonly flow: ManualInterventionFlow) {}

  public getStepType(): string {
    return "Init";
  }

  public execute(context: Context, _input: void): StepDecision {
    this.flow.numberOfRetries.set(context, 0);
    return goTo(this.flow.getDataStep, false);
  }
}

class GetData implements Step<boolean> {
  public readonly inputCodec = booleanCodec;

  public constructor(private readonly flow: ManualInterventionFlow) {}

  public getStepType(): string {
    return "GetData";
  }

  public waitFor(_context: Context, _isRetry: boolean): Wait {
    console.log("Waiting for incoming data");
    return Wait.until(this.flow.dataChannel.forOne());
  }

  public execute(context: Context, isRetry: boolean): StepDecision {
    if (isRetry) {
      const retries = this.flow.numberOfRetries.get(context);
      this.flow.numberOfRetries.set(context, retries + 1);
    }
    try {
      this.pretendApiCall(context);
    } catch {
      return goTo(this.flow.errorStep, undefined);
    }
    return goTo(this.flow.finalStep, undefined);
  }

  private pretendApiCall(context: Context): void {
    const results = this.flow.dataChannel.results(context);
    if (results.length > 0) {
      const data = results[0];
      console.log(`Received data result: ${data}`);
      if (data === "failed") {
        throw new Error("Non-retryable exception");
      }
    }
  }
}

class ErrorStep implements Step<void> {
  public constructor(private readonly flow: ManualInterventionFlow) {}

  public getStepType(): string {
    return "Error";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(this.flow.retrySignal.forOne(), this.flow.skipSignal.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const retry = this.flow.retrySignal.results(context).length > 0;
    console.log(
      `signal received: ${retry ? SIGNAL_CHANNEL_COMMAND_RETRY : SIGNAL_CHANNEL_COMMAND_SKIP}`,
    );
    if (retry) {
      return goTo(this.flow.getDataStep, true);
    }
    return goTo(this.flow.finalStep, undefined);
  }
}

class Final implements Step<void> {
  public constructor(private readonly flow: ManualInterventionFlow) {}

  public getStepType(): string {
    return "Final";
  }

  public execute(context: Context, _input: void): StepDecision {
    const retries = this.flow.numberOfRetries.get(context);
    return gracefulComplete(`Workflow Completed. Number of retries: ${retries}`);
  }
}

export class ManualInterventionFlow implements Flow<void> {
  public readonly dataChannel = new Channel(INTERNAL_CHANNEL_COMMAND, stringCodec);
  public readonly retrySignal = new Channel(SIGNAL_CHANNEL_COMMAND_RETRY, voidCodec);
  public readonly skipSignal = new Channel(SIGNAL_CHANNEL_COMMAND_SKIP, voidCodec);
  public readonly numberOfRetries = new Attribute(NUMBER_OF_RETRIES, doubleCodec);

  private readonly initStep = new Init(this);
  private readonly getData = new GetData(this);
  private readonly error = new ErrorStep(this);
  private readonly final = new Final(this);

  public get getDataStep(): Step<boolean> {
    return this.getData;
  }

  public get errorStep(): Step<void> {
    return this.error;
  }

  public get finalStep(): Step<void> {
    return this.final;
  }

  public getFlowType(): string {
    return "ManualInterventionFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initStep).otherSteps(
      this.getData,
      this.error,
      this.final,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.numberOfRetries],
      channels: [this.dataChannel, this.retrySignal, this.skipSignal],
    };
  }
}

export const manualInterventionFlow = new ManualInterventionFlow();
