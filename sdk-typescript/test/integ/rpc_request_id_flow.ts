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
  booleanCodec,
  doubleCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
} from "../../src/index.js";

const total = new Attribute("total", doubleCodec);
const processed = new Attribute("processed", booleanCodec);

export class RpcRequestIdFlow implements Flow {
  public readonly total = total;
  public readonly processed = processed;
  public transactionalHandlerCalls = 0;
  public nonTransactionalHandlerCalls = 0;

  public getFlowType(): string {
    return "RpcRequestIdFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.total, this.processed] };
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

  @rpc({ outputCodec: stringCodec })
  public markProcessedNonTransactional(context: Context): RPCResult<string> {
    return this.markProcessed(context);
  }

  @rpc({ outputCodec: stringCodec, isTransactional: true })
  public markProcessedTransactional(context: Context): RPCResult<string> {
    return this.markProcessed(context);
  }

  private markProcessed(context: Context): RPCResult<string> {
    if (this.processed.get(context) === true) {
      return { output: "alreadyProcessed" };
    }
    this.processed.set(context, true);
    return { output: "accepted" };
  }

  @rpc({ outputCodec: doubleCodec })
  public getTotal(context: Context): RPCResult<number> {
    return { output: this.total.get(context) ?? 0 };
  }
}
