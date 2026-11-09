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
  declareRPCMethod,
  forceComplete,
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
const approvePermission = "refund.approve";

interface RejectRefundInput {
  reason: string;
  gate_request_key: string;
  note?: string;
}

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Complete the Flow after the review."
class FinishStep implements Step<unknown> {
  public getStepType(): string {
    return "FinishStep";
  }

  public execute(_context: Context, _input: unknown): StepDecision {
    return forceComplete();
  }
}

export class RefundReviewFlow implements Flow<unknown> {
  private readonly finish = new FinishStep();

  public getSteps() {
    return StepList.startStep(this.finish);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {attributes: [caseStatus, gateRequestKey]};
  }

  // dex:input field-name:reason value-type:string source:user required:true description:"Rejection reason"
  // dex:input field-name:gate_request_key value-type:string source:attribute attribute-key:gate-request-key required:true description:"Approval gate"
  // dex:input field-name:note value-type:string source:user required:false description:"Note"
  @rpc({
    lockAttributes: [caseStatus.lock()],
    action: new Action({
      label: "Reject refund",
      requiredPermission: approvePermission,
      condition: whenAttributeMatches(caseStatus, AttributeMatch.equalTo("awaiting-manager"), AttributeMatch.equalTo("escalated")),
    }),
  })
  public rejectRefund(context: Context, input: RejectRefundInput): RPCResult<void> {
    caseStatus.set(context, `rejected: ${input.reason}`);
    return {output: undefined};
  }

  @rpc({
    name: "cancel_review",
    action: new Action({label: "Cancel review", requiredPermission: "refund.cancel"}),
  })
  public cancelReview(context: Context): RPCResult<void> {
    caseStatus.set(context, "canceled");
    return {output: undefined};
  }

  public describeStatus(_context: Context): RPCResult<string> {
    return {output: "status"};
  }
}

declareRPCMethod(RefundReviewFlow, "describeStatus", {name: "describe_status"});
