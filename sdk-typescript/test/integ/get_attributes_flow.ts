// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  Attribute,
  AttributeMap,
  StepList,
  int64Codec,
  jsonCodec,
  stringCodec,
  type Flow,
  type PersistenceSchema,
} from "../../src/index.js";

export interface GetAttributesNote {
  readonly text: string;
  readonly priority: number;
}

export const getAttributesStatus = new Attribute("get-attributes-status", stringCodec);
export const getAttributesEmptyText = new Attribute("get-attributes-empty-text", stringCodec);
export const getAttributesCount = new Attribute("get-attributes-count", int64Codec);
export const getAttributesZero = new Attribute("get-attributes-zero", int64Codec);
export const getAttributesUnset = new Attribute("get-attributes-unset", int64Codec);
export const getAttributesNote = new Attribute("get-attributes-note", jsonCodec<GetAttributesNote>());
export const getAttributesNullNote = new Attribute(
  "get-attributes-null-note",
  jsonCodec<GetAttributesNote | null>(),
);
export const getAttributesSecret = new Attribute("get-attributes-secret", stringCodec).hardDeleteOnNull();
export const getAttributesLargeValue = new Attribute("get-attributes-large-value", stringCodec);
export const getAttributesQuantities = new AttributeMap("get-attributes-quantities", int64Codec);

export class GetAttributesFlow implements Flow<void> {
  public getFlowType(): string {
    return "GetAttributesFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [
        getAttributesStatus,
        getAttributesEmptyText,
        getAttributesCount,
        getAttributesZero,
        getAttributesUnset,
        getAttributesNote,
        getAttributesNullNote,
        getAttributesSecret,
        getAttributesLargeValue,
        getAttributesQuantities,
      ],
    };
  }
}
