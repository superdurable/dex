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
  Attribute,
  Channel,
  IndexType,
  StepDef,
  StepMovement,
  Wait,
  doubleCodec,
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

class RpcSecondStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "RpcSecondStep";
  }

  public execute(_context: Context, input: number): StepDecision {
    return gracefulComplete(input + 1);
  }
}

class RpcFirstStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly internal: Channel<void>,
    private readonly second: RpcSecondStep,
  ) {}

  public getStepType(): string {
    return "RpcFirstStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.anyOf(this.internal.forOne());
  }

  public execute(_context: Context, _input: number): StepDecision {
    return goTo(this.second, 0);
  }
}

export class RpcFlow implements Flow<number> {
  public readonly internal = new Channel("rpc-internal", voidCodec);
  public readonly data = new Attribute("rpc-data", stringCodec);
  public readonly keyword = new Attribute("rpc-keyword", stringCodec, {
    type: IndexType.KEYWORD,
  });
  private readonly second = new RpcSecondStep();
  private readonly first = new RpcFirstStep(this.internal, this.second);

  public getFlowType(): string {
    return "RpcFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.first), StepDef.nonStartStep(this.second)];
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.data, this.keyword], channels: [this.internal] };
  }

  @rpc()
  public noPersistence(context: Context): void {
    this.internal.publish(context, undefined);
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public functionOne(context: Context, input: string): RPCResult<number> {
    this.data.set(context, input);
    this.keyword.set(context, input);
    return { output: 1, nextSteps: [StepMovement.of(this.second, 0)] };
  }

  @rpc({ outputCodec: doubleCodec })
  public functionZero(_context: Context): RPCResult<number> {
    return { output: 1, nextSteps: [StepMovement.of(this.second, 0)] };
  }

  @rpc({ inputCodec: stringCodec })
  public procedureOne(context: Context, input: string): void {
    this.data.set(context, input);
  }

  @rpc()
  public procedureZero(context: Context): void {
    this.internal.publish(context, undefined);
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public readOnly(_context: Context, input: string): RPCResult<number> {
    return { output: input.length };
  }

  @rpc({ inputCodec: stringCodec })
  public setData(context: Context, input: string): void {
    this.data.set(context, input);
  }

  @rpc({ outputCodec: stringCodec })
  public getData(context: Context): RPCResult<string> {
    return { output: this.data.get(context) };
  }

  @rpc({ inputCodec: stringCodec })
  public setKeyword(context: Context, input: string): void {
    this.keyword.set(context, input);
  }

  @rpc({ outputCodec: stringCodec })
  public getKeyword(context: Context): RPCResult<string> {
    return { output: this.keyword.get(context) };
  }
}
