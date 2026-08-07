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
  StepMovement,
  Wait,
  goTo,
  goToMulti,
  gracefulComplete,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RetryPolicy,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

class OptionsThirdStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly bothValue: Attribute<string>) {}

  public getStepType(): string {
    return "OptionsThirdStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("success");
  }

  public getStepOptions(): StepOptions {
    return {
      waitForLockAttributes: [this.bothValue.lock()],
      executeLockAttributes: [this.bothValue.lock()],
    };
  }
}

class OptionsSecondStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(
    private readonly third: OptionsThirdStep,
    private readonly waitValue: Attribute<string>,
    private readonly executeValue: Attribute<string>,
    private readonly bothValue: Attribute<string>,
  ) {}

  public getStepType(): string {
    return "OptionsSecondStep";
  }

  public waitFor(context: Context, _input: void): Wait {
    this.waitValue.set(context, "wait");
    this.bothValue.set(context, "wait");
    return Wait.skipImmediately();
  }

  public execute(context: Context, _input: void): StepDecision {
    this.executeValue.set(context, "execute");
    this.bothValue.set(context, "execute");
    return goToMulti(
      StepMovement.of(this.third, undefined, { executeMethodTimeoutMs: 2_000 }),
    );
  }

  public getStepOptions(): StepOptions {
    const retry: RetryPolicy = {
      initialIntervalMs: 10,
      maximumAttempts: 3,
    };
    return {
      waitForMethodTimeoutMs: 1_000,
      executeMethodTimeoutMs: 1_000,
      waitForRetry: retry,
      executeRetry: retry,
      waitForDurability: "sync",
      executeDurability: "async",
      waitForLockAttributes: [this.waitValue.lock()],
      executeLockAttributes: [this.executeValue.lock()],
    };
  }
}

class OptionsFirstStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly second: OptionsSecondStep) {}

  public getStepType(): string {
    return "OptionsFirstStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(this.second, undefined);
  }
}

export class StateOptionsFlow implements Flow {
  public readonly waitValue = new Attribute("DA_WAIT_UNTIL", stringCodec);
  public readonly executeValue = new Attribute("DA_EXECUTE", stringCodec);
  public readonly bothValue = new Attribute("DA_BOTH", stringCodec);
  private readonly third = new OptionsThirdStep(this.bothValue);
  private readonly second = new OptionsSecondStep(
    this.third,
    this.waitValue,
    this.executeValue,
    this.bothValue,
  );
  private readonly first = new OptionsFirstStep(this.second);

  public getFlowType(): string {
    return "StateOptionsFlow";
  }

  public getSteps() {
    return StepList.startStep(this.first).otherSteps(this.second, this.third);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.waitValue, this.executeValue, this.bothValue] };
  }
}
