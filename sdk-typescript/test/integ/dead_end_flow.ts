// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
