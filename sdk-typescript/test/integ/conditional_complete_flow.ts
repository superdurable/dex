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
  StepList,
  StepMovement,
  Wait,
  booleanCodec,
  doubleCodec,
  forceCompleteWhenChannelsEmpty,
  rpc,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class ConditionalStep implements Step<boolean> {
  public readonly inputCodec = booleanCodec;

  public constructor(
    private readonly counter: Attribute<number>,
    private readonly signal: Channel<void>,
    private readonly internal: Channel<void>,
  ) {}

  public getStepType(): string {
    return "ConditionalStep";
  }

  public waitFor(_context: Context, useSignal: boolean): Wait {
    return Wait.anyOf((useSignal ? this.signal : this.internal).forOne());
  }

  public execute(context: Context, useSignal: boolean): StepDecision {
    const next = this.counter.get(context) + 1;
    this.counter.set(context, next);
    const selected = useSignal ? this.signal : this.internal;
    return forceCompleteWhenChannelsEmpty(
      next,
      StepMovement.of(this, useSignal),
      selected,
    );
  }
}

export class ConditionalCompleteFlow implements Flow<boolean> {
  public readonly signal = new Channel("test-signal-channel", voidCodec);
  public readonly internal = new Channel("test-internal-channel", voidCodec);
  private readonly counter = new Attribute("counter", doubleCodec);
  private readonly start = new ConditionalStep(this.counter, this.signal, this.internal);

  public getFlowType(): string {
    return "ConditionalCompleteFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.counter], channels: [this.signal, this.internal] };
  }

  @rpc()
  public publishToInternalChannel(context: Context): void {
    this.internal.publish(context, undefined);
  }
}
