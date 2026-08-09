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
  gracefulComplete,
  rpc,
  stringCodec,
  voidCodec,
  type Client,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../../src/index.js";

// Child flow started by an async parent Step; echoes its input back.
class EchoStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "EchoStep";
  }

  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(input);
  }
}

export class EchoFlow implements Flow<string> {
  private readonly start = new EchoStep();

  public getFlowType(): string {
    return "AsyncEchoFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

// Parent whose Step awaits Client.startFlow + Client.waitForFlow on the same Worker.
class StartAndWaitStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: AsyncStartAndWaitFlow) {}

  public getStepType(): string {
    return "StartAndWaitStep";
  }

  public async execute(_context: Context, childId: string): Promise<StepDecision> {
    await this.flow.client.startFlow(this.flow.child, childId, `child-of-${childId}`);
    const childOutput = await this.flow.client.waitForFlow(childId, stringCodec, 30_000);
    return gracefulComplete(childOutput);
  }
}

export class AsyncStartAndWaitFlow implements Flow<string> {
  public client!: Client;
  public readonly child = new EchoFlow();
  private readonly start = new StartAndWaitStep(this);

  public getFlowType(): string {
    return "AsyncStartAndWaitFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

// RPC target: stays running on a channel wait; echo RPC returns its input.
class RpcEchoStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly proceed: Channel<void>) {}

  public getStepType(): string {
    return "RpcEchoStep";
  }

  public waitFor(): Wait {
    return Wait.until(this.proceed.forOne());
  }

  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(input);
  }
}

export class RpcEchoFlow implements Flow<string> {
  public readonly proceed = new Channel("async-rpc-proceed", voidCodec);
  private readonly start = new RpcEchoStep(this.proceed);

  public getFlowType(): string {
    return "AsyncRpcEchoFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.proceed] };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public echo(_context: Context, input: string): RPCResult<string> {
    return { output: input };
  }
}

// Parent whose Step awaits Client.invokeRPC against a flow on the same Worker.
class InvokeRpcStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: AsyncRpcFlow) {}

  public getStepType(): string {
    return "InvokeRpcStep";
  }

  public async execute(_context: Context, targetId: string): Promise<StepDecision> {
    await this.flow.client.startFlow(this.flow.target, targetId, "start");
    const echoed = await this.flow.client.invokeRPC(this.flow.target.echo, targetId, "hello");
    return gracefulComplete(`rpc-echo:${echoed}`);
  }
}

export class AsyncRpcFlow implements Flow<string> {
  public client!: Client;
  public readonly target = new RpcEchoFlow();
  private readonly start = new InvokeRpcStep(this);

  public getFlowType(): string {
    return "AsyncRpcParentFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

// Step whose waitFor resolves a Wait asynchronously.
class AsyncWaitStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "AsyncWaitStep";
  }

  public async waitFor(): Promise<Wait> {
    await Promise.resolve();
    return Wait.skipImmediately();
  }

  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(input);
  }
}

export class AsyncWaitForFlow implements Flow<string> {
  private readonly start = new AsyncWaitStep();

  public getFlowType(): string {
    return "AsyncWaitForFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}
