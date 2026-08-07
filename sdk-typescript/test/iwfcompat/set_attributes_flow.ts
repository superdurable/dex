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
  booleanCodec,
  doubleCodec,
  stringCodec,
  type Flow,
  type PersistenceSchema,
} from "../../src/index.js";

import { CompleteStringStep, dateCodec, stringArrayCodec } from "./shared.js";

export class SetAttributesFlow implements Flow<string> {
  public readonly data = new Attribute("data", stringCodec);
  public readonly dataMap = new AttributeMap("data-map", stringCodec);
  public readonly keyword = new Attribute("keyword", stringCodec, {
    type: IndexType.KEYWORD,
  });
  public readonly text = new Attribute("text", stringCodec, {
    type: IndexType.FULL_TEXT,
  });
  public readonly decimal = new Attribute("double", doubleCodec, {
    type: IndexType.DOUBLE,
  });
  public readonly integer = new Attribute("int", doubleCodec, {
    type: IndexType.INT,
  });
  public readonly bool = new Attribute("bool", booleanCodec, {
    type: IndexType.BOOL,
  });
  public readonly keywords = new Attribute("keywords", stringArrayCodec, {
    type: IndexType.KEYWORD_ARRAY,
  });
  public readonly datetime = new Attribute("datetime", dateCodec, {
    type: IndexType.DATETIME,
  });
  private readonly start = new CompleteStringStep();

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
        this.keyword,
        this.text,
        this.decimal,
        this.integer,
        this.bool,
        this.keywords,
        this.datetime,
      ],
    };
  }
}
