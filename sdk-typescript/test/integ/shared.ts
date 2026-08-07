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
  gracefulComplete,
  jsonCodec,
  stringCodec,
  type Context,
  type Step,
  type StepDecision,
} from "../../src/index.js";

export interface ModelInput {
  value: number;
}

export const modelInputCodec = jsonCodec<ModelInput>({
  typeName: "ModelInput",
  decode: (value) => {
    if (typeof value !== "object" || value === null || typeof (value as ModelInput).value !== "number") {
      throw new TypeError("invalid ModelInput");
    }
    return value as ModelInput;
  },
  encode: (value) => {
    if (typeof value !== "object" || value === null || typeof value.value !== "number") {
      throw new TypeError("invalid ModelInput");
    }
    return value;
  },
});

export const dateCodec = jsonCodec<Date>({
  typeName: "Date",
  decode: (value) => new Date(String(value)),
  encode: (value) => value.toISOString(),
});

export const stringArrayCodec = jsonCodec<readonly string[]>({
  typeName: "string[]",
  decode: (value) => value as readonly string[],
});

export class CompleteStringStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "CompleteStringStep";
  }

  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(input);
  }
}
