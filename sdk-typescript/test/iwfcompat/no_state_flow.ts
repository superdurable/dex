// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  Attribute,
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
    return [];
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
