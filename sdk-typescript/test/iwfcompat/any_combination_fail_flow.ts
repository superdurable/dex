// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  Channel,
  ConditionCombination,
  StepDef,
  Timer,
  Wait,
  doubleCodec,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

class AnyCombinationStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly first: Channel<number>,
    private readonly second: Channel<number>,
    private readonly third: Channel<number>,
  ) {}

  public getStepType(): string {
    return "AnyCombinationStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.anyCombinationOf(
      ConditionCombination.of(
        this.first.forOne("test-signal-1"),
        Timer.byDuration(1_000, "test-timer-id"),
      ),
      ConditionCombination.of(
        this.second.forOne("test-signal-2"),
        this.third.forOne("test-signal-3"),
      ),
    );
  }

  public execute(_context: Context, input: number): StepDecision {
    return gracefulComplete(input);
  }

  public getStepOptions(): StepOptions {
    return { waitForMethodTimeoutMs: 1_000 };
  }
}

export class AnyCombinationFailFlow implements Flow<number> {
  private readonly first = new Channel("test-signal-1", doubleCodec);
  private readonly second = new Channel("test-signal-2", doubleCodec);
  private readonly third = new Channel("test-signal-3", doubleCodec);
  private readonly start = new AnyCombinationStep(this.first, this.second, this.third);

  public getFlowType(): string {
    return "AnyCombinationFailFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.start)];
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.first, this.second, this.third] };
  }
}
