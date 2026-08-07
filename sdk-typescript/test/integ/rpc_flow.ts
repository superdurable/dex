// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  Attribute,
  Channel,
  IndexType,
  StepList,
  Wait,
  doubleCodec,
  goTo,
  gracefulComplete,
  optionalCodec,
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
    return gracefulComplete(2);
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
  public static readonly RPC_OUTPUT = 100;
  public static readonly HARDCODED_VALUE = "random-string";
  public readonly internal = new Channel("rpc-internal", voidCodec);
  public readonly data = new Attribute("rpc-data", optionalCodec(stringCodec));
  public readonly keyword = new Attribute("CustomKeywordField", optionalCodec(stringCodec), {
    type: IndexType.KEYWORD,
  });
  public readonly integer = new Attribute("CustomIntField", doubleCodec, {
    type: IndexType.INT,
  });
  private readonly second = new RpcSecondStep();
  private readonly first = new RpcFirstStep(this.internal, this.second);

  public getFlowType(): string {
    return "RpcFlow";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.data, this.keyword, this.integer],
      channels: [this.internal],
    };
  }

  @rpc()
  public noPersistence(context: Context): void {
    this.requireContext(context);
    this.internal.publish(context, undefined);
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public functionOne(context: Context, input: string): RPCResult<number> {
    this.requireContext(context);
    this.data.set(context, undefined);
    this.data.set(context, input);
    this.keyword.set(context, input);
    this.integer.set(context, RpcFlow.RPC_OUTPUT);
    this.internal.publish(context, undefined);
    return { output: RpcFlow.RPC_OUTPUT };
  }

  @rpc({ outputCodec: doubleCodec })
  public functionZero(_context: Context): RPCResult<number> {
    this.requireContext(_context);
    this.data.set(_context, RpcFlow.HARDCODED_VALUE);
    this.keyword.set(_context, RpcFlow.HARDCODED_VALUE);
    this.integer.set(_context, RpcFlow.RPC_OUTPUT);
    this.internal.publish(_context, undefined);
    return { output: RpcFlow.RPC_OUTPUT };
  }

  @rpc({ inputCodec: stringCodec })
  public procedureOne(context: Context, input: string): void {
    this.requireContext(context);
    this.data.set(context, input);
    this.keyword.set(context, input);
    this.integer.set(context, RpcFlow.RPC_OUTPUT);
    this.internal.publish(context, undefined);
  }

  @rpc()
  public procedureZero(context: Context): void {
    this.requireContext(context);
    this.data.set(context, RpcFlow.HARDCODED_VALUE);
    this.keyword.set(context, RpcFlow.HARDCODED_VALUE);
    this.integer.set(context, RpcFlow.RPC_OUTPUT);
    this.internal.publish(context, undefined);
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public readOnly(_context: Context, input: string): RPCResult<number> {
    this.requireContext(_context);
    return { output: RpcFlow.RPC_OUTPUT };
  }

  @rpc({ inputCodec: optionalCodec(stringCodec) })
  public setData(context: Context, input: string | undefined): void {
    this.requireContext(context);
    this.data.set(context, input);
  }

  @rpc({ outputCodec: optionalCodec(stringCodec) })
  public getData(context: Context): RPCResult<string | undefined> {
    this.requireContext(context);
    return { output: this.data.get(context) };
  }

  @rpc({ inputCodec: optionalCodec(stringCodec) })
  public setKeyword(context: Context, input: string | undefined): void {
    this.requireContext(context);
    this.keyword.set(context, input);
  }

  @rpc({ outputCodec: optionalCodec(stringCodec) })
  public getKeyword(context: Context): RPCResult<string | undefined> {
    this.requireContext(context);
    return { output: this.keyword.get(context) };
  }

  private requireContext(context: Context): void {
    if (context.flowId === "" || context.runId === "") {
      throw new Error("invalid RPC context");
    }
  }
}
