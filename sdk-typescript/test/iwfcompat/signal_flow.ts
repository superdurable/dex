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
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class SignalCombinationStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly second: Channel<number>,
    private readonly third: Channel<number>,
    private readonly signalMap: ChannelMap<number>,
  ) {}

  public getStepType(): string {
    return "SignalCombinationStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.anyCombinationOf(
      ConditionCombination.of(
        this.second.forOne("signal-2"),
        Timer.byDuration(10_000, "test-timer-id"),
      ),
      ConditionCombination.of(this.third.forN(2), this.signalMap.forOne("one")),
    );
  }

  public execute(context: Context, input: number): StepDecision {
    return gracefulComplete(input + this.third.size(context));
  }
}

class SignalFirstStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly first: Channel<number>,
    private readonly combination: SignalCombinationStep,
  ) {}

  public getStepType(): string {
    return "SignalFirstStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.anyOf(this.first.forOne("test-signal-id"));
  }

  public execute(context: Context, input: number): StepDecision {
    return goTo(this.combination, input + (this.first.results(context)[0] ?? 0));
  }
}

export class SignalFlow implements Flow<number> {
  public readonly first = new Channel("signal-1", doubleCodec);
  public readonly second = new Channel("signal-2", doubleCodec);
  public readonly third = new Channel("signal-3", doubleCodec);
  public readonly signalMap = new ChannelMap("signal-map", doubleCodec);
  private readonly combination = new SignalCombinationStep(
    this.second,
    this.third,
    this.signalMap,
  );
  private readonly start = new SignalFirstStep(this.first, this.combination);

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
