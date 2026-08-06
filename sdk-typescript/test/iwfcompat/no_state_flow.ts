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
  StepList,
  doubleCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
} from "../../src/index.js";

const counter = new Attribute("counter", doubleCodec);

export class NoStateFlow implements Flow {
  public readonly counter = counter;

  public getFlowType(): string {
    return "NoStateFlow";
  }

  public getSteps() {
    return StepList.withoutStartStep<void>();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.counter] };
  }

  @rpc({ outputCodec: doubleCodec, lockAttributes: [counter.lock()] })
  public increaseCounter(context: Context): RPCResult<number> {
    const next = this.counter.get(context) + 1;
    this.counter.set(context, next);
    return { output: next };
  }

  @rpc({ outputCodec: doubleCodec })
  public getCounter(context: Context): RPCResult<number> {
    return { output: this.counter.get(context) };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public fail(_context: Context, input: string): RPCResult<number> {
    throw new Error(input);
  }
}
