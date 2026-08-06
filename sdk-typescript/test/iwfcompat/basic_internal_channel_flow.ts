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
  StepDef,
  StepMovement,
  Wait,
  deadEnd,
  doubleCodec,
  goToMulti,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class ConsumeStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly first: Channel<number>,
    private readonly channelMap: ChannelMap<number>,
  ) {}

  public getStepType(): string {
    return "ConsumeStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.anyCombinationOf(
      ConditionCombination.of(this.first.forOne("first")),
      ConditionCombination.of(this.channelMap.forOne("one")),
    );
  }

  public execute(context: Context, input: number): StepDecision {
    return gracefulComplete(input + this.first.results(context).length);
  }
}

class PublishStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly first: Channel<number>,
    private readonly channelMap: ChannelMap<number>,
  ) {}

  public getStepType(): string {
    return "PublishStep";
  }

  public execute(context: Context, input: number): StepDecision {
    this.first.publish(context, input);
    this.channelMap.publish(context, "one", input);
    return deadEnd();
  }
}

class ForkStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(
    private readonly consumer: ConsumeStep,
    private readonly publisher: PublishStep,
  ) {}

  public getStepType(): string {
    return "ForkStep";
  }

  public execute(_context: Context, input: number): StepDecision {
    return goToMulti(
      StepMovement.of(this.consumer, input),
      StepMovement.of(this.publisher, input),
    );
  }
}

export class BasicInternalChannelFlow implements Flow<number> {
  private readonly firstChannel = new Channel("test-inter-state-channel-1", doubleCodec);
  private readonly channelMap = new ChannelMap("test-inter-state-channel-map", doubleCodec);
  private readonly consumer = new ConsumeStep(this.firstChannel, this.channelMap);
  private readonly publisher = new PublishStep(this.firstChannel, this.channelMap);
  private readonly start = new ForkStep(this.consumer, this.publisher);

  public getFlowType(): string {
    return "BasicInternalChannelFlow";
  }

  public getSteps() {
    return [
      StepDef.startStep(this.start),
      StepDef.nonStartStep(this.consumer),
      StepDef.nonStartStep(this.publisher),
    ];
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.firstChannel, this.channelMap] };
  }
}
