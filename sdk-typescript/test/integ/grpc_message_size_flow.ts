// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

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
