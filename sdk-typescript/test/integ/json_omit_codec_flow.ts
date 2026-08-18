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
  rpc,
  type Context,
  type Flow,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../../src/index.js";

export interface JsonOrder {
  readonly orderId: string;
}

class CompleteJsonOrder implements Step<JsonOrder> {
  public getStepType(): string {
    return "CompleteJsonOrder";
  }

  public execute(_context: Context, input: JsonOrder): StepDecision {
    return gracefulComplete(input);
  }
}

export class JsonOmitStepFlow implements Flow<JsonOrder> {
  public readonly start = new CompleteJsonOrder();

  public getFlowType(): string {
    return "JsonOmitStepFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}

export class JsonOmitRpcFlow implements Flow {
  public getFlowType(): string {
    return "JsonOmitRpcFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  @rpc()
  public describe(_context: Context, order: JsonOrder): RPCResult<JsonOrder> {
    return { output: order };
  }
}
