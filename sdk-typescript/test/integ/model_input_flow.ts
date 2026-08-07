// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  StepList,
  gracefulComplete,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../../src/index.js";

import { modelInputCodec, type ModelInput } from "./shared.js";

class ModelInputStep implements Step<ModelInput> {
  public readonly inputCodec = modelInputCodec;

  public getStepType(): string {
    return "ModelInputStep";
  }

  public execute(_context: Context, input: ModelInput): StepDecision {
    return gracefulComplete(input.value);
  }
}

export class ModelInputFlow implements Flow<ModelInput> {
  private readonly start = new ModelInputStep();

  public getFlowType(): string {
    return "ModelInputFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}
