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
  Wait,
  goTo,
  gracefulComplete,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
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
    if (this.bothValue.get(_context) !== "both") {
      throw new Error("shared attribute was not loaded in execute");
    }
    return gracefulComplete("success");
  }

  public waitFor(context: Context, _input: void): Wait {
    if (this.bothValue.get(context) !== "both") {
      throw new Error("shared attribute was not loaded in waitFor");
    }
    return Wait.skipImmediately();
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
    if (this.waitValue.get(context) !== "wait_until") {
      throw new Error("waitFor attribute was not loaded");
    }
    if (this.executeValue.get(context) !== "execute") {
      throw new Error("execute attribute was not loaded in waitFor");
    }
    if (this.bothValue.get(context) !== "both") {
      throw new Error("shared attribute was not loaded in waitFor");
    }
    return Wait.skipImmediately();
  }

  public execute(context: Context, _input: void): StepDecision {
    if (this.executeValue.get(context) !== "execute") {
      throw new Error("execute attribute was not loaded");
    }
    if (this.waitValue.get(context) !== "wait_until") {
      throw new Error("waitFor attribute was not loaded in execute");
    }
    if (this.bothValue.get(context) !== "both") {
      throw new Error("shared attribute was not loaded in execute");
    }
    return goTo(OptionsThirdStep, undefined);
  }

  public getStepOptions(): StepOptions {
    return {
      waitForLockAttributes: [this.waitValue.lock()],
      executeLockAttributes: [this.executeValue.lock()],
    };
  }
}

class OptionsFirstStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(
    private readonly second: OptionsSecondStep,
    private readonly waitValue: Attribute<string>,
    private readonly executeValue: Attribute<string>,
    private readonly bothValue: Attribute<string>,
  ) {}

  public getStepType(): string {
    return "OptionsFirstStep";
  }

  public execute(context: Context, _input: void): StepDecision {
    this.executeValue.set(context, "execute");
    this.waitValue.set(context, "wait_until");
    this.bothValue.set(context, "both");
    return goTo(OptionsSecondStep, undefined);
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
  private readonly first = new OptionsFirstStep(
    this.second,
    this.waitValue,
    this.executeValue,
    this.bothValue,
  );

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
