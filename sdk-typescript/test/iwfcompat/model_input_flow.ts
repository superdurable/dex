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
  StepDef,
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
    return [StepDef.startStep(this.start)];
  }
}
