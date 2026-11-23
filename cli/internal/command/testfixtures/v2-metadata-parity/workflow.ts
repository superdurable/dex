// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Attribute,
  IndexType,
  StepList,
  declareRPCMethod,
  forceComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

const stateIndex = {type: IndexType.KEYWORD};
// dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:"Current state"
const state = new Attribute("state", stringCodec, stateIndex);
const label = new Attribute("label", stringCodec);

type Priority = "Normal" | "Urgent";

interface SchedulingDetails {
  due: Date;
}

type MetadataInput = {
  priority: Priority;
  note?: string | null;
  tags: string[] | null;
  details: Record<string, SchedulingDetails> | null;
  mode: "Preview" | "Apply";
};

// dex:group group-id:finish group-label:"Finish"
// dex:explanation text:"Finish the Flow."
class FinishMetadataStep implements Step<MetadataInput> {
  public getStepType(): string {
    return "FinishMetadataStep";
  }

  public execute(_context: Context, _input: MetadataInput): StepDecision {
    return forceComplete();
  }
}

export class MetadataFlow implements Flow<MetadataInput> {
  public getFlowType(): string {
    return "MetadataFlow";
  }

  public getSteps() {
    return StepList.startStep(new FinishMetadataStep());
  }

  public getPersistenceSchema() {
    return {attributes: [state, label]};
  }

  // dex:field attribute-key:label value-type:string editable:false description:"Case label"
  @rpc({name: "GetDexSummary"})
  public summary(context: Context): RPCResult<Record<string, string>> {
    return {output: {label: label.get(context)}};
  }

  // dex:field attribute-key:state value-type:string editable:false description:"Current state" ui-slot:status
  public display(context: Context): RPCResult<Record<string, string>> {
    return {output: displayOutput(context)};
  }
}

declareRPCMethod(MetadataFlow, "display", {name: "GetDexDisplay"});

function displayOutput(context: Context): Record<string, string> {
  return {state: state.get(context)};
}
