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
  StepDef,
  deadEnd,
  doubleCodec,
  rpc,
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

export class DeadEndFlow implements Flow {
  public readonly idleSignal = new Channel("idle-signal", voidCodec);
  private readonly start = new DeadEndStep();

  public getFlowType(): string {
    return "DeadEndFlow";
  }

  public getSteps() {
    return [StepDef.startStep(this.start)];
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [this.idleSignal] };
  }

  @rpc({ outputCodec: doubleCodec })
  public signalSize(context: Context): RPCResult<number> {
    return { output: this.idleSignal.size(context) };
  }

  @rpc({ outputCodec: doubleCodec })
  public publishInternal(context: Context): RPCResult<number> {
    this.idleSignal.publish(context, undefined);
    return { output: this.idleSignal.size(context) };
  }
}
