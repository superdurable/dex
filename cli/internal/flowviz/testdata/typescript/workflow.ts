// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Attribute,
  StepList,
  Stream,
  forceComplete,
  jsonCodec,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";
import { createLookup as lookupRecord, createTrigger } from "@dex-test/catalog";

const result = new Attribute("LookupResult", jsonCodec<Record<string, unknown>>());
const progress = new Stream("LookupProgress", jsonCodec<Record<string, unknown>>(), 65_536);
const text = new Stream("LookupText", stringCodec, 65_536);
const connectionName = "primary";

// dex:group group-id:lookup group-label:"Lookup"
// dex:explanation text:"Complete the Flow after the lookup."
class FinishStep implements Step<unknown> {
  public getStepType(): string {
    return "FinishStep";
  }

  public execute(_context: Context, _input: unknown): StepDecision {
    return forceComplete();
  }
}

export class CatalogFlow implements Flow<unknown> {
  private readonly finish = new FinishStep();
  private readonly lookup = lookupRecord({
    stepType: "LookupRecord",
    connectionName,
    annotations: {groupId: "lookup", groupLabel: "Lookup", explanation: "Look up a catalog record."},
    executeFailure: this.finish,
    found: this.finish,
    unavailable: this.finish,
    resultAttribute: result,
    progressStream: progress,
    textStream: text,
  });

  public getFlowType(): string {
    return "CatalogFlow";
  }

  public getSteps() {
    return StepList.startStep(this.lookup).otherSteps(this.finish);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {attributes: [result], streams: [progress, text]};
  }

  public getConnectorTriggerBindings() {
    return [createTrigger({connectionName, bindingName: "updates"})];
  }
}
