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
  Channel,
  ChannelMap,
  ConditionCombination,
  StepList,
  Timer,
  Wait,
  doubleCodec,
  goTo,
  gracefulComplete,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class SignalCombinationStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly first: Channel<number>,
    private readonly second: Channel<number>,
    private readonly third: Channel<void>,
    private readonly signalMap: ChannelMap<number>,
  ) {}

  public getStepType(): string {
    return "SignalCombinationStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.anyCombinationOf(
      ConditionCombination.of(
        this.first.forOne("signal-1"),
        this.third.forOne("signal-3"),
        this.signalMap.forOne("one"),
        Timer.byDuration(365 * 24 * 60 * 60 * 1_000, "test-timer-id"),
      ),
    );
  }

  public execute(context: Context, input: number): StepDecision {
    if (this.second.results(context).length !== 0) {
      throw new Error("second signal should still be waiting");
    }
    if (this.third.results(context).length !== 1) {
      throw new Error("null signal was not received");
    }
    if (this.signalMap.results(context, "one").length !== 1) {
      throw new Error("mapped signal was not received");
    }
    if (!context.hasTimerFired()) {
      throw new Error("timer was not fired");
    }
    return gracefulComplete(input + (this.first.results(context)[0] ?? 0));
  }
}

class SignalFirstStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly first: Channel<number>,
    private readonly second: Channel<number>,
    private readonly combination: SignalCombinationStep,
  ) {}

  public getStepType(): string {
    return "SignalFirstStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.anyOf(
      this.first.forOne("test-signal-id-1"),
      this.second.forOne("test-signal-id-2"),
    );
  }

  public execute(context: Context, input: number): StepDecision {
    if (this.second.results(context).length !== 0) {
      throw new Error("second signal should still be waiting");
    }
    return goTo(this.combination, input + (this.first.results(context)[0] ?? 0));
  }
}

export class SignalFlow implements Flow<number> {
  public readonly first = new Channel("signal-1", doubleCodec);
  public readonly second = new Channel("signal-2", doubleCodec);
  public readonly third = new Channel("signal-3", voidCodec);
  public readonly signalMap = new ChannelMap("signal-map", doubleCodec);
  private readonly combination = new SignalCombinationStep(
    this.first,
    this.second,
    this.third,
    this.signalMap,
  );
  private readonly start = new SignalFirstStep(this.first, this.second, this.combination);

  public getFlowType(): string {
    return "SignalFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.combination);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.first, this.second, this.third, this.signalMap] };
  }
}
