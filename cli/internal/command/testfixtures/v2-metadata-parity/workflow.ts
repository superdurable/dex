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
  Channel,
  IndexType,
  StepList,
  bytesCodec,
  declareRPCMethod,
  forceComplete,
  int64Codec,
  jsonCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";
import type {ExternalLabel} from "./external-label.js";

const stateIndex = {type: IndexType.KEYWORD, indexKey: ""};
// dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:"Current state"
const state = new Attribute("state", stringCodec, stateIndex);
// dex:indexed-attribute attribute-key:total index-key:case-total index-type:int value-type:int64 description:"Total cases"
const total = new Attribute("total", int64Codec, {type: IndexType.INT, indexKey: "case-total"});
// dex:indexed-attribute attribute-key:due index-key:due index-type:datetime value-type:datetime description:"Due date"
const due = new Attribute("due", jsonCodec<Date>(), {type: IndexType.DATETIME});
// dex:indexed-attribute attribute-key:external-label index-key:external-label index-type:keyword value-type:string description:"External label"
const externalLabel = new Attribute<ExternalLabel>("external-label", stringCodec, {type: IndexType.KEYWORD});
const label = new Attribute("label", stringCodec);
const labels = new AttributeMap("labels", stringCodec);
const details = new Attribute("details", jsonCodec<SchedulingDetails>());
const options = new Attribute<SchedulingOptions>("options");
const priority = new Attribute("priority", jsonCodec<Priority>());
const externalPriority = new Attribute<IndexType>("external-priority");
const payload = new Attribute("payload", bytesCodec);
const events = new Channel("events", stringCodec);

enum Priority {
  Normal = "Normal",
  Urgent = "Urgent",
}

interface SchedulingDetails {
  due: Date;
}

type SchedulingOptions = {due: Date};

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
    return {attributes: [state, total, due, externalLabel, label, labels, details, options, priority, externalPriority, payload], channels: [events]};
  }

  // dex:field attribute-key:label value-type:string editable:false description:"Case label"
  // dex:field attribute-key:labels value-type:attribute-map editable:false description:"Labels by case"
  // dex:field attribute-key:details value-type:json editable:false description:"Scheduling details"
  // dex:field attribute-key:options value-type:json editable:false description:"Scheduling options"
  // dex:field attribute-key:priority value-type:json editable:false description:"Priority"
  // dex:field attribute-key:external-priority value-type:json editable:false description:"External priority"
  // dex:field attribute-key:payload value-type:array editable:false description:"Case payload"
  @rpc({name: "GetDexSummary", loadAttributeMaps: [labels]})
  public summary(context: Context): RPCResult<Record<string, unknown>> {
    total.get(context);
    due.get(context);
    externalLabel.get(context);
    events.size(context);
    return {output: {
      label: label.get(context), labels: labels.get(context, "case"),
      details: details.get(context), options: options.get(context),
      priority: priority.get(context), "external-priority": externalPriority.get(context),
      payload: payload.get(context),
    }};
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
