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
  doubleCodec,
  rpc,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
} from "../../src/index.js";

const total = new Attribute("total", doubleCodec);

export class RpcRequestIdFlow implements Flow {
  public readonly total = total;
  public transactionalHandlerCalls = 0;
  public nonTransactionalHandlerCalls = 0;

  public getFlowType(): string {
    return "RpcRequestIdFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.total] };
  }

  @rpc({ inputCodec: doubleCodec, outputCodec: doubleCodec, isTransactional: true })
  public addTransactional(context: Context, amount: number): RPCResult<number> {
    this.transactionalHandlerCalls++;
    const next = (this.total.get(context) ?? 0) + amount;
    this.total.set(context, next);
    return { output: next };
  }

  @rpc({ inputCodec: doubleCodec, outputCodec: doubleCodec })
  public addNonTransactional(context: Context, amount: number): RPCResult<number> {
    this.nonTransactionalHandlerCalls++;
    const next = (this.total.get(context) ?? 0) + amount;
    this.total.set(context, next);
    return { output: next };
  }

  @rpc({ outputCodec: doubleCodec })
  public getTotal(context: Context): RPCResult<number> {
    return { output: this.total.get(context) ?? 0 };
  }
}
