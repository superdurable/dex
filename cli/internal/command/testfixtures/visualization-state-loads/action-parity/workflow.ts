// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Action,
  Attribute,
  AttributeMatch,
  StepList,
  gracefulComplete,
  int64Codec,
  rpc,
  stringCodec,
  whenAttributeMatches,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

const caseStatus = new Attribute("case-status", stringCodec);
const gateRequestKey = new Attribute("gate-request-key", stringCodec);
const priority = new Attribute("priority", int64Codec);

interface RejectRefundInput {
  reason: string;
  gate_request_key: string;
  note?: string;
}

// dex:group group-id:hold group-label:"Hold"
// dex:explanation text:"Wait for the review to close."
class HoldStep implements Step<unknown> {
  public getStepType(): string {
    return "HoldStep";
  }

  public execute(_context: Context, _input: unknown): StepDecision {
    return gracefulComplete();
  }
}

export class RefundReviewFlow implements Flow<unknown> {
  private readonly hold = new HoldStep();

  public getSteps() {
    return StepList.startStep(this.hold);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {attributes: [caseStatus, gateRequestKey, priority]};
  }

  // dex:input field-name:reason value-type:string source:user required:true description:"Rejection reason"
  // dex:input field-name:gate_request_key value-type:string source:attribute attribute-key:gate-request-key required:true description:"Approval gate"
  // dex:input field-name:note value-type:string source:user required:false description:"Note"
  @rpc({
    name: "reject_refund",
    lockAttributes: [caseStatus.lock()],
    action: new Action({
      label: "Reject refund",
      requiredPermission: "refund.approve",
      condition: whenAttributeMatches(caseStatus, AttributeMatch.equalTo("awaiting-manager"), AttributeMatch.equalTo("escalated")),
    }),
  })
  public rejectRefund(context: Context, input: RejectRefundInput): RPCResult<void> {
    if (caseStatus.get(context) === "awaiting-manager") {
      caseStatus.set(context, `rejected: ${input.reason}`);
    }
    return {output: undefined};
  }

  @rpc({
    name: "escalate_case",
    action: new Action({
      label: "Escalate",
      requiredPermission: "refund.escalate",
      condition: whenAttributeMatches(priority, AttributeMatch.equalTo(1n), AttributeMatch.equalTo(2n)),
    }),
  })
  public escalateCase(context: Context): RPCResult<void> {
    if (priority.get(context) !== undefined) {
      caseStatus.set(context, "escalated");
    }
    return {output: undefined};
  }
}
