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
  StepMovement,
  doubleCodec,
  gracefulComplete,
  rpc,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class TriggeredStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "TriggeredStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete(1);
  }
}

export class NoStartFlow implements Flow {
  private readonly triggered = new TriggeredStep();

  public getFlowType(): string {
    return "NoStartFlow";
  }

  public getSteps() {
    return StepList.withoutStartStep<void>(this.triggered);
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public invoke(_context: Context, _input: string): RPCResult<number> {
    if (_context.flowId === "" || _context.runId === "") {
      throw new Error("invalid RPC context");
    }
    return { output: 100, nextSteps: [StepMovement.of(this.triggered, undefined)] };
  }
}
