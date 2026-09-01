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
  Wait,
  gracefulComplete,
  stringCodec,
  voidCodec,
  type AsyncContext,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class StreamTestStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(
    private readonly progress: Stream<string>,
    private readonly details: Stream<string>,
  ) {}

  public getStepType(): string {
    return "StreamTestStep";
  }

  public getStepOptions() {
    return { heartbeatTimeoutMs: 10_000 };
  }

  public async waitFor(context: AsyncContext, _input: void): Promise<Wait> {
    await context.recordHeartbeat("wait-for", stringCodec);
    const progress = this.progress.bufferedText(context, {
      maxBufferedBytes: "wait-progress-1".length,
    });
    progress.write("wait-progress-");
    progress.write("1");
    progress.write("wait-progress-");
    progress.write("2");
    this.details.write(context, "wait-details");
    return Wait.skipImmediately();
  }

  public async execute(context: AsyncContext, _input: void): Promise<StepDecision> {
    await context.recordHeartbeat({ phase: "execute" });
    const progress = this.progress.bufferedText(context, {
      flushIntervalMs: 500,
      maxBufferedBytes: "execute-progress-1".length,
    });
    progress.write("execute-progress-");
    progress.write("1");
    this.details.write(context, "execute-details");
    progress.write("execute-progress-");
    progress.write("2");
    return gracefulComplete();
  }
}

export class StreamTestFlow implements Flow<void> {
  public readonly progress = new Stream("stream-test-progress", stringCodec, 1 << 20);
  public readonly details = new Stream("stream-test-details", stringCodec, 1 << 20);
  private readonly start = new StreamTestStep(this.progress, this.details);

  public getFlowType(): string {
    return "StreamTestFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { streams: [this.progress, this.details] };
  }
}
