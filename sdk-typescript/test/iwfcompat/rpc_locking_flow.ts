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
  StepDef,
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
const counter = new Attribute("rpc-lock-counter", doubleCodec, {
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
  public readonly counter = counter;
  public readonly items = items;
  private readonly second = new LockCompleteStep();
  private readonly first = new LockWaitStep(this.channel, this.second);

  public getFlowType(): string {
    return "RpcLockingFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.first), StepDef.nonStartStep(this.second)];
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.data, this.counter, this.items],
      channels: [this.channel],
    };
  }

  @rpc({ lockAttributes: [data.lock(), counter.lock()] })
  public withLocking(context: Context): void {
    this.data.set(context, "locked");
    this.counter.set(context, 1);
    this.channel.publish(context, undefined);
  }

  @rpc({ lockAttributes: [items.lock("order-1")] })
  public withAttributeMapLock(context: Context): void {
    this.items.set(context, "order-1", "locked");
  }

  @rpc()
  public withoutLocking(context: Context): void {
    this.channel.publish(context, undefined);
  }
}
