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
  StepList,
  Stream,
  gracefulComplete,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class StreamTestStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly progress: Stream<string>) {}

  public getStepType(): string {
    return "StreamTestStep";
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    await this.progress.write(context, "step-progress");
    return gracefulComplete();
  }
}

export class StreamTestFlow implements Flow<void> {
  public readonly progress = new Stream("stream-test-progress", stringCodec, 1 << 20);
  private readonly start = new StreamTestStep(this.progress);

  public getFlowType(): string {
    return "StreamTestFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { streams: [this.progress] };
  }
}
