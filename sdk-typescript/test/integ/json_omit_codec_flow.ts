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
