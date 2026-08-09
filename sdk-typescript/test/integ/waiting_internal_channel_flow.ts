// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  Channel,
  StepList,
  Wait,
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class WaitingInternalStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly channel: Channel<number>) {}

  public getStepType(): string {
    return "WaitingInternalStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.until(this.channel.forN(2));
  }

  public execute(context: Context, input: number): StepDecision {
    const output = this.channel.results(context).reduce((sum, value) => sum + value, input);
    return gracefulComplete(output);
  }
}

export class WaitingInternalChannelFlow implements Flow<number> {
  public readonly channel = new Channel("waiting-channel", doubleCodec);
  private readonly start = new WaitingInternalStep(this.channel);

  public getFlowType(): string {
    return "WaitingInternalChannelFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.channel] };
  }
}
