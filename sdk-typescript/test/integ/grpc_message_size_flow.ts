// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  Attribute,
  StepList,
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

export const fiveMebibytes = 5 * 1024 * 1024;

export const grpcSizeValues = [0, 1, 2, 3].map((index) => new Attribute(`grpc-size-value-${index}`, stringCodec));

class SumValueLengthsStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "SumValueLengthsStep";
  }

  public execute(context: Context): StepDecision {
    const total = grpcSizeValues.reduce((sum, value) => sum + value.get(context).length, 0);
    return gracefulComplete(String(total));
  }
}

export class GrpcMessageSizeFlow implements Flow<void> {
  private readonly start = new SumValueLengthsStep();

  public getFlowType(): string {
    return "GrpcMessageSizeFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: grpcSizeValues };
  }
}

export class LargeRpcFlow implements Flow<void> {
  public getFlowType(): string {
    return "LargeRpcFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }

  @rpc({ outputCodec: stringCodec })
  public readLargeText(_context: Context): RPCResult<string> {
    return { output: "r".repeat(fiveMebibytes) };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public measureText(_context: Context, input: string): RPCResult<string> {
    return { output: String(input.length) };
  }
}

/** Three values whose sum exceeds the server's default 32 MiB send limit. */
export const aboveServerLimitValueBytes = 12 * 1024 * 1024;

export const aboveServerLimitValues = [
  new Attribute("grpc-size-above-server-limit-0", stringCodec),
  new Attribute("grpc-size-above-server-limit-1", stringCodec),
  new Attribute("grpc-size-above-server-limit-2", stringCodec),
] as const;

export class AboveServerLimitFlow implements Flow<void> {
  public getFlowType(): string {
    return "AboveServerLimitFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: aboveServerLimitValues };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: voidCodec })
  public writeLargeValue(context: Context, input: string): RPCResult<void> {
    aboveServerLimitValues[Number(input) as 0 | 1 | 2].set(context, input.repeat(aboveServerLimitValueBytes));
    return { output: undefined };
  }
}
