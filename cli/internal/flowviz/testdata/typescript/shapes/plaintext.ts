// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Attribute,
  AttributeMap,
  StepList,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

const status = new Attribute("Status", stringCodec).plaintext();
const reviews = new AttributeMap("Reviews", stringCodec).plaintext();

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Record the review status in plaintext Attributes."
class RecordStatus implements Step<string> {
  getStepType(): string {
    return "RecordStatus";
  }

  execute(context: Context, input: string): StepDecision {
    status.set(context, input);
    reviews.set(context, "latest", input);
    return gracefulComplete();
  }
}

export class StatusFlow implements Flow<string> {
  private readonly record = new RecordStatus();

  getFlowType(): string {
    return "StatusFlow";
  }

  getSteps() {
    return StepList.startStep(this.record);
  }

  getPersistenceSchema(): PersistenceSchema {
    return { attributes: [status, reviews] };
  }

  @rpc({ outputCodec: stringCodec })
  getStatus(context: Context): RPCResult<string> {
    return { output: status.get(context) };
  }
}

export const statusFlow = new StatusFlow();
