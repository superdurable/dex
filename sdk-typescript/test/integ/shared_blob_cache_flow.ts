// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  Attribute,
  StepList,
  StepMovement,
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

/** Holds a text large enough that Dex stores it as a blob and sends a reference. */
export const sharedBlobCacheText = new Attribute("shared-blob-cache-text", stringCodec);

class ReportTextLengthStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "ReportTextLengthStep";
  }

  public execute(context: Context, _input: void): StepDecision {
    return gracefulComplete(String(sharedBlobCacheText.get(context).length));
  }
}

export class SharedBlobCacheFlow implements Flow<void> {
  private readonly report = new ReportTextLengthStep();

  public getFlowType(): string {
    return "SharedBlobCacheFlow";
  }

  public getSteps() {
    return StepList.withoutStartStep<void>(this.report);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [sharedBlobCacheText] };
  }

  @rpc({ outputCodec: stringCodec })
  public readTextLength(context: Context): RPCResult<string> {
    return { output: String(sharedBlobCacheText.get(context).length) };
  }

  @rpc({ outputCodec: voidCodec })
  public reportTextLength(_context: Context): RPCResult<void> {
    return { output: undefined, nextSteps: [StepMovement.of(ReportTextLengthStep, undefined)] };
  }
}
