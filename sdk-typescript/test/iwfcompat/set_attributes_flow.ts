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
  Channel,
  IndexType,
  StepList,
  booleanCodec,
  doubleCodec,
  stringCodec,
  voidCodec,
  Wait,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

import { dateCodec, modelInputCodec, stringArrayCodec, type ModelInput } from "./shared.js";

class SetAttributesCompleteStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly proceed: Channel<void>) {}

  public getStepType(): string {
    return "SetAttributesCompleteStep";
  }

  public waitFor(_context: Context, _input: string): Wait {
    return Wait.allOf(this.proceed.forOne());
  }

  public execute(_context: Context, _input: string): StepDecision {
    return gracefulComplete("test-result");
  }
}

export class SetAttributesFlow implements Flow<string> {
  public readonly data = new Attribute("data", stringCodec);
  public readonly dataMap = new AttributeMap("data-map", stringCodec);
  public readonly model = new Attribute<ModelInput>("data-model", modelInputCodec);
  public readonly keyword = new Attribute("CustomKeywordField", stringCodec, {
    type: IndexType.KEYWORD,
  });
  public readonly text = new Attribute("CustomTextField", stringCodec, {
    type: IndexType.FULL_TEXT,
  });
  public readonly decimal = new Attribute("CustomDoubleField", doubleCodec, {
    type: IndexType.DOUBLE,
  });
  public readonly integer = new Attribute("CustomIntField", doubleCodec, {
    type: IndexType.INT,
  });
  public readonly bool = new Attribute("CustomBoolField", booleanCodec, {
    type: IndexType.BOOL,
  });
  public readonly keywords = new Attribute("CustomKeywordArrayField", stringArrayCodec, {
    type: IndexType.KEYWORD_ARRAY,
  });
  public readonly datetime = new Attribute("CustomDatetimeField", dateCodec, {
    type: IndexType.DATETIME,
  });
  public readonly proceed = new Channel("proceed", voidCodec);
  private readonly start = new SetAttributesCompleteStep(this.proceed);

  public getFlowType(): string {
    return "SetAttributesFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [
        this.data,
        this.dataMap,
        this.model,
        this.keyword,
        this.text,
        this.decimal,
        this.integer,
        this.bool,
        this.keywords,
        this.datetime,
      ],
      channels: [this.proceed],
    };
  }
}
