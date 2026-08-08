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
} from "../../src/index.js";

// Sync waitFor + async execute, then hands off to an async-wait step.
class SyncWaitAsyncExecuteStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly next: AsyncWaitSyncExecuteStep) {}

  public getStepType(): string {
    return "SyncWaitAsyncExecuteStep";
  }

  public waitFor(context: Context, input: string): Wait {
    context.setStepExecutionLocal("prefix", input, stringCodec);
    return Wait.skipImmediately();
  }

  public async execute(context: Context, input: string): Promise<StepDecision> {
    await Promise.resolve();
    const prefix = context.getStepExecutionLocal("prefix", stringCodec) ?? input;
    return goTo(this.next, `${prefix}-async-exec`);
  }
}

// Async waitFor + sync execute, then hands off to a fully sync step.
class AsyncWaitSyncExecuteStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly next: SyncCompleteStep) {}

  public getStepType(): string {
    return "AsyncWaitSyncExecuteStep";
  }

  public async waitFor(): Promise<Wait> {
    await Promise.resolve();
    return Wait.skipImmediately();
  }

  public execute(_context: Context, input: string): StepDecision {
    return goTo(this.next, `${input}-sync-exec`);
  }
}

class SyncCompleteStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "SyncCompleteStep";
  }

  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(`${input}-done`);
  }
}

export class MixedSyncAsyncStepsFlow implements Flow<string> {
  private readonly third = new SyncCompleteStep();
  private readonly second = new AsyncWaitSyncExecuteStep(this.third);
  private readonly first = new SyncWaitAsyncExecuteStep(this.second);

  public getFlowType(): string {
    return "MixedSyncAsyncStepsFlow";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second, this.third);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

// Sync Step waits on a channel; async RPC wakes it and returns a value.
class SyncChannelWaitStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly proceed: Channel<void>) {}

  public getStepType(): string {
    return "SyncChannelWaitStep";
  }

  public waitFor(): Wait {
    return Wait.anyOf(this.proceed.forOne());
  }

  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(input);
  }
}

export class MixedSyncStepAsyncRpcFlow implements Flow<string> {
  public readonly proceed = new Channel("mixed-sync-async-proceed", voidCodec);
  private readonly start = new SyncChannelWaitStep(this.proceed);

  public getFlowType(): string {
    return "MixedSyncStepAsyncRpcFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.proceed] };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public async wake(context: Context, input: string): Promise<RPCResult<string>> {
    await Promise.resolve();
    this.proceed.publish(context, undefined);
    return { output: `woke:${input}` };
  }
}
