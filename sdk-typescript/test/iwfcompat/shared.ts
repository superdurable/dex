// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
  decode: (value) => value as ModelInput,
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
