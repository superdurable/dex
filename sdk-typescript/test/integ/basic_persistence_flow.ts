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
  AttributeMap,
  IndexType,
  StepList,
  Wait,
  doubleCodec,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

import { dateCodec, modelInputCodec, type ModelInput } from "./shared.js";

class PersistenceStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: BasicPersistenceFlow) {}

  public getStepType(): string {
    return "PersistenceStep";
  }

  public waitFor(context: Context, input: string): Wait {
    this.flow.data.set(context, input);
    this.flow.dataMap.set(context, "one", input);
    context.setStepExecutionLocal("local", input, stringCodec);
    context.recordEvent("written", input, stringCodec);
    return Wait.skipImmediately();
  }

  public execute(context: Context, input: string): StepDecision {
    if (context.getStepExecutionLocal("local", stringCodec) !== input) {
      throw new Error("step execution local did not survive waitFor");
    }
    this.flow.keyword.set(context, input);
    this.flow.integer.set(context, 1);
    this.flow.datetime.set(context, new Date("2023-04-17T21:17:49Z"));
    this.flow.model.set(context, { value: 0 });
    this.flow.dataMap.delete(context, "one");
    return gracefulComplete(this.flow.data.get(context));
  }
}

export class BasicPersistenceFlow implements Flow<string> {
  public readonly initial = new Attribute("data-obj-0", stringCodec);
  public readonly data = new Attribute("data-obj-1", stringCodec);
  public readonly model = new Attribute<ModelInput>("data-obj-2", modelInputCodec);
  public readonly dataMap = new AttributeMap("data-map", stringCodec);
  public readonly keyword = new Attribute("CustomKeywordField", stringCodec, {
    type: IndexType.KEYWORD,
  });
  public readonly integer = new Attribute("CustomIntField", doubleCodec, {
    type: IndexType.INT,
  });
  public readonly datetime = new Attribute("CustomDatetimeField", dateCodec, {
    type: IndexType.DATETIME,
  });
  private readonly start = new PersistenceStep(this);

  public getFlowType(): string {
    return "BasicPersistenceFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [
        this.initial,
        this.data,
        this.model,
        this.dataMap,
        this.keyword,
        this.integer,
        this.datetime,
      ],
    };
  }
}
