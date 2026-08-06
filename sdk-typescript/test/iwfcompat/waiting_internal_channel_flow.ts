// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  Channel,
  StepDef,
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
    return Wait.allOf(this.channel.forN(2));
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
    return [StepDef.startStep(this.start)];
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.channel] };
  }
}
