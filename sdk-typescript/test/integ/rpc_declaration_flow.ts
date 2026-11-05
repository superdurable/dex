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
  declareRPCMethod,
  doubleCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
} from "../../src/index.js";

const declaredValue = new Attribute("declared-value", stringCodec);
const declaredWriteCount = new Attribute("declared-write-count", doubleCodec);

export abstract class DeclaredRpcBaseFlow implements Flow {
  public readonly declaredValue = declaredValue;
  public readonly declaredWriteCount = declaredWriteCount;

  public abstract getFlowType(): string;

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [declaredValue, declaredWriteCount] };
  }

  public getDeclaredValue(context: Context): RPCResult<string> {
    return { output: declaredValue.get(context) };
  }

  public setDeclaredValue(context: Context, value: string): RPCResult<string> {
    declaredValue.set(context, value);
    declaredWriteCount.set(context, (declaredWriteCount.get(context) ?? 0) + 1);
    return { output: value };
  }
}

export class DeclaredRpcFlow extends DeclaredRpcBaseFlow {
  public getFlowType(): string {
    return "DeclaredRpcFlow";
  }

  public describeDeclaredRpcFlow(context: Context): RPCResult<string> {
    return { output: `${context.flowId}:${declaredValue.get(context) ?? ""}` };
  }

  @rpc({ outputCodec: doubleCodec })
  public getDeclaredWriteCount(context: Context): RPCResult<number> {
    return { output: declaredWriteCount.get(context) ?? 0 };
  }
}

// Declared through the base class: every subclass inherits these RPCs.
declareRPCMethod(DeclaredRpcBaseFlow, "getDeclaredValue", { outputCodec: stringCodec });
declareRPCMethod(DeclaredRpcBaseFlow, "setDeclaredValue", {
  inputCodec: stringCodec,
  outputCodec: stringCodec,
  lockAttributes: [declaredValue.lock(), declaredWriteCount.lock()],
});
declareRPCMethod(DeclaredRpcFlow, "describeDeclaredRpcFlow", {
  name: "describeFlowWithDeclaredName",
  outputCodec: stringCodec,
  timeoutMs: 10_000,
});
