// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  Attribute,
  AttributeMap,
  Channel,
  IndexType,
  StepList,
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
  type Step,
  type StepDecision,
} from "../../src/index.js";

const data = new Attribute("rpc-lock-data", stringCodec);
const keyword = new Attribute("CustomKeywordField", stringCodec, {
  type: IndexType.KEYWORD,
});
const counter = new Attribute("CustomIntField", doubleCodec, {
  type: IndexType.INT,
});
const items = new AttributeMap("rpc-lock-items", stringCodec);

class LockCompleteStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockCompleteStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("lock complete");
  }
}

class LockWaitStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(
    private readonly channel: Channel<void>,
    private readonly second: LockCompleteStep,
  ) {}

  public getStepType(): string {
    return "LockWaitStep";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(this.channel.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(this.second, undefined);
  }
}

export class RpcLockingFlow implements Flow {
  public readonly channel = new Channel("rpc-channel", voidCodec);
  public readonly data = data;
  public readonly keyword = keyword;
  public readonly counter = counter;
  public readonly items = items;
  private readonly second = new LockCompleteStep();
  private readonly first = new LockWaitStep(this.channel, this.second);

  public getFlowType(): string {
    return "RpcLockingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.data, this.keyword, this.counter, this.items],
      channels: [this.channel],
    };
  }

  @rpc({ lockAttributes: [data.lock(), keyword.lock(), counter.lock()] })
  public withLocking(context: Context): void {
    this.writeAttributes(context);
    this.channel.publish(context, undefined);
  }

  @rpc({ lockAttributes: [items.lock("order-1")] })
  public withAttributeMapLock(context: Context): void {
    this.items.set(context, "order-1", "locked");
  }

  @rpc()
  public withoutLocking(context: Context): void {
    this.writeAttributes(context);
    this.channel.publish(context, undefined);
  }

  private writeAttributes(context: Context): void {
    this.data.set(context, "random-string");
    this.keyword.set(context, "random-string");
    this.counter.set(context, 100);
  }
}
