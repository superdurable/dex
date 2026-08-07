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
  Channel,
  StepList,
  StepMovement,
  deadEnd,
  doubleCodec,
  gracefulComplete,
  rpc,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../../src/index.js";

class DeadEndStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "DeadEndStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return deadEnd();
  }
}

class DeadEndCompleteStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "DeadEndCompleteStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete();
  }
}

export class DeadEndFlow implements Flow {
  public readonly idleSignal = new Channel("idle-signal", voidCodec);
  public readonly idleInternal = new Channel("idle-internal", voidCodec);
  private readonly start = new DeadEndStep();
  private readonly complete = new DeadEndCompleteStep();

  public getFlowType(): string {
    return "DeadEndFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.complete);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.idleSignal, this.idleInternal] };
  }

  @rpc({ outputCodec: doubleCodec })
  public signalSize(context: Context): RPCResult<number> {
    return { output: this.idleSignal.size(context) };
  }

  @rpc({ outputCodec: doubleCodec })
  public publishInternal(context: Context): RPCResult<number> {
    this.idleInternal.publish(context, undefined);
    return { output: this.idleInternal.size(context) };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public invoke(context: Context, _input: string): RPCResult<number> {
    if (context.flowId === "" || context.runId === "") {
      throw new Error("invalid RPC context");
    }
    return { output: 100, nextSteps: [StepMovement.of(this.complete, undefined)] };
  }
}
