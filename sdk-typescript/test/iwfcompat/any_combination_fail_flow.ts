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
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
  type Wait,
} from "../../src/index.js";

class AnyCombinationStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly first: Channel<number>,
    private readonly second: Channel<number>,
    private readonly third: Channel<number>,
  ) {}

  public getStepType(): string {
    return "AnyCombinationStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    throw new Error("Found unknown condition ID in the combination list");
  }

  public execute(_context: Context, input: number): StepDecision {
    return gracefulComplete(input);
  }

  public getStepOptions(): StepOptions {
    return { waitForRetry: { maximumAttempts: 1 } };
  }
}

export class AnyCombinationFailFlow implements Flow<number> {
  private readonly first = new Channel("test-signal-1", doubleCodec);
  private readonly second = new Channel("test-signal-2", doubleCodec);
  private readonly third = new Channel("test-signal-3", doubleCodec);
  private readonly start = new AnyCombinationStep(this.first, this.second, this.third);

  public getFlowType(): string {
    return "AnyCombinationFailFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.first, this.second, this.third] };
  }
}
