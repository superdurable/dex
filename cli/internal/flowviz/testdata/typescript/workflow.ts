// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Attribute, StepDecision, StepList, Stream, type Flow, type Step } from "@superdurable/dex";
import { createLookup as lookupRecord, createTrigger } from "@dex-test/catalog";

const result = new Attribute<Record<string, unknown>>("LookupResult");
const progress = new Stream<Record<string, unknown>>("LookupProgress");
const text = new Stream<string>("LookupText");
const connectionName = "primary";

// dex:group group-id:lookup group-label:"Lookup"
// dex:explanation text:"Complete the Flow after the lookup."
class FinishStep implements Step {
  execute() { return StepDecision.forceComplete(); }
}

export class CatalogFlow implements Flow {
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

  getFlowType() { return "CatalogFlow"; }
  getSteps() { return StepList.startStep(this.lookup).otherSteps(this.finish); }
  getConnectorTriggerBindings() {
    return [createTrigger({connectionName, bindingName: "updates"})];
  }
}
