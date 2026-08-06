// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  Attribute,
  AttributeMap,
  IndexType,
  StepDef,
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
    return [StepDef.startStep(this.start)];
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
