/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/** CustomerRefundFlow: check a refund request, ask a manager when needed, refund, and tell the customer. */

import {
  Action,
  Attribute,
  AttributeMatch,
  Channel,
  StepList,
  Wait,
  goTo,
  gracefulComplete,
  jsonCodec,
  rpc,
  stringCodec,
  whenAttributeMatches,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import type { FakeBillingService, Order } from "./billing-service.js";
import type { FakeEmailService } from "./email-service.js";
import type { MockMessageModel } from "./message-model.js";

export const REFUND_WINDOW_DAYS = 30;
export const MANAGER_APPROVAL_LIMIT_USD = 1000.0;
export const AWAITING_MANAGER = "awaiting-manager";
export const REFUND_NOT_AWAITING_MANAGER_ERROR_TYPE = "RefundNotAwaitingManagerError";
export const MANAGE_REFUNDS_PERMISSION = "refund.manage";
export const WINDOW_EXPIRED_NOTE = "The charge is outside the 30-day refund window.";
export const NO_OUTAGE_NOTE = "No service outage was recorded for this charge.";

// Readable without the payload key, so Dex Web can show and match them.
export const caseStatus = new Attribute("case-status", stringCodec).plaintext();
export const customerEmail = new Attribute("customer-email", stringCodec).plaintext();
// Encrypted when the Worker has a payload key.
export const refundId = new Attribute("refund-id", stringCodec);
export const customerMessage = new Attribute("customer-message", stringCodec);

export interface RefundRequest {
  readonly customer: string;
  readonly customerEmail: string;
  readonly orderId: string;
  readonly reason: string;
  readonly paymentTimeoutsToSimulate?: number;
}

export interface ManagerDecision {
  readonly approved: boolean;
  readonly reason: string;
}

export interface DecisionReason {
  reason: string;
}

export interface RefundCase {
  readonly request: RefundRequest;
  readonly order: Order;
  readonly outcome?: string;
  readonly note?: string;
  readonly refundId?: string;
  readonly message?: string;
}

export const managerDecisions = new Channel("manager-decisions", jsonCodec<ManagerDecision>());

// dex:group group-id:intake group-label:"Intake"
// dex:explanation text:"Record the refund request and the customer's email address."
class ReceiveRequest implements Step<RefundRequest> {
  public getStepType(): string {
    return "ReceiveRequest";
  }

  public execute(context: Context, input: RefundRequest): StepDecision {
    caseStatus.set(context, "received");
    customerEmail.set(context, input.customerEmail);
    return goTo(CheckOrder, input);
  }
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Look up the order in billing."
class CheckOrder implements Step<RefundRequest> {
  public constructor(private readonly billing: FakeBillingService) {}

  public getStepType(): string {
    return "CheckOrder";
  }

  public execute(_context: Context, input: RefundRequest): StepDecision {
    const order = this.billing.getOrder(input.orderId);
    return goTo(CheckPolicy, { request: input, order });
  }
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Refund, deny, or ask a manager, by the refund policy."
class CheckPolicy implements Step<RefundCase> {
  public getStepType(): string {
    return "CheckPolicy";
  }

  public execute(context: Context, input: RefundCase): StepDecision {
    const order = input.order;
    const windowExpired = order.daysSinceCharge > REFUND_WINDOW_DAYS;
    const noOutage = order.outageHours === 0;
    if (windowExpired || noOutage) {
      caseStatus.set(context, "denied");
      const notes = [
        windowExpired ? WINDOW_EXPIRED_NOTE : "",
        noOutage ? NO_OUTAGE_NOTE : "",
      ];
      return goTo(WriteCustomerMessage, { ...input, outcome: "denied", note: notes.join(" ").trim() });
    }
    if (order.amountUsd > MANAGER_APPROVAL_LIMIT_USD) {
      caseStatus.set(context, AWAITING_MANAGER);
      return goTo(RequestManagerApproval, input);
    }
    return goTo(IssueRefund, input);
  }
}

// dex:group group-id:control group-label:"Control"
// dex:explanation text:"Wait for a manager to approve or reject the refund."
class RequestManagerApproval implements Step<RefundCase> {
  public getStepType(): string {
    return "RequestManagerApproval";
  }

  public waitFor(_context: Context, _input: RefundCase): Wait {
    return Wait.until(managerDecisions.forOne());
  }

  public execute(context: Context, input: RefundCase): StepDecision {
    const [decision] = managerDecisions.results(context);
    if (decision.approved) {
      return goTo(IssueRefund, input);
    }
    caseStatus.set(context, "denied");
    return goTo(WriteCustomerMessage, { ...input, outcome: "denied", note: decision.reason });
  }
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Refund the order through billing, once per case."
class IssueRefund implements Step<RefundCase> {
  public constructor(private readonly billing: FakeBillingService) {}

  public getStepType(): string {
    return "IssueRefund";
  }

  public execute(context: Context, input: RefundCase): StepDecision {
    const simulateTimeout = context.attempt <= (input.request.paymentTimeoutsToSimulate ?? 0);
    const issuedRefundId = this.billing.refund(input.order, `refund-${context.flowId}`, simulateTimeout);
    refundId.set(context, issuedRefundId);
    caseStatus.set(context, "refunded");
    return goTo(WriteCustomerMessage, { ...input, outcome: "refunded", refundId: issuedRefundId });
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: { initialIntervalMs: 5_000, backoffCoefficient: 1.0, maximumAttempts: 10 },
    };
  }
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Ask the model to write the customer message."
class WriteCustomerMessage implements Step<RefundCase> {
  public constructor(private readonly model: MockMessageModel) {}

  public getStepType(): string {
    return "WriteCustomerMessage";
  }

  public execute(context: Context, input: RefundCase): StepDecision {
    const message = this.model.writeMessage(
      input.request.customer,
      input.outcome ?? "",
      input.order.amountUsd,
      input.note ?? "",
    );
    customerMessage.set(context, message);
    return goTo(SendMessage, { ...input, message });
  }
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Email the message to the customer, once per case."
class SendMessage implements Step<RefundCase> {
  public constructor(private readonly email: FakeEmailService) {}

  public getStepType(): string {
    return "SendMessage";
  }

  public execute(context: Context, input: RefundCase): StepDecision {
    this.email.send(input.request.customerEmail, input.message ?? "", `email-${context.flowId}`);
    return goTo(CloseCase, input);
  }
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Close the case with its outcome."
class CloseCase implements Step<RefundCase> {
  public getStepType(): string {
    return "CloseCase";
  }

  public execute(context: Context, input: RefundCase): StepDecision {
    caseStatus.set(context, `closed-${input.outcome}`);
    return gracefulComplete(input.outcome);
  }
}

class RefundNotAwaitingManagerError extends Error {
  public constructor() {
    super("this refund is not waiting for a manager");
    this.name = REFUND_NOT_AWAITING_MANAGER_ERROR_TYPE;
  }
}

export class CustomerRefundFlow implements Flow<RefundRequest> {
  private readonly receiveRequest: ReceiveRequest;
  private readonly checkOrder: CheckOrder;
  private readonly checkPolicy: CheckPolicy;
  private readonly requestManagerApproval: RequestManagerApproval;
  private readonly issueRefund: IssueRefund;
  private readonly writeCustomerMessage: WriteCustomerMessage;
  private readonly sendMessage: SendMessage;
  private readonly closeCase: CloseCase;

  public constructor(billing: FakeBillingService, email: FakeEmailService, model: MockMessageModel) {
    this.receiveRequest = new ReceiveRequest();
    this.checkOrder = new CheckOrder(billing);
    this.checkPolicy = new CheckPolicy();
    this.requestManagerApproval = new RequestManagerApproval();
    this.issueRefund = new IssueRefund(billing);
    this.writeCustomerMessage = new WriteCustomerMessage(model);
    this.sendMessage = new SendMessage(email);
    this.closeCase = new CloseCase();
  }

  public getFlowType(): string {
    return "CustomerRefundFlow";
  }

  public getSteps() {
    return StepList.startStep(this.receiveRequest).otherSteps(
      this.checkOrder,
      this.checkPolicy,
      this.requestManagerApproval,
      this.issueRefund,
      this.writeCustomerMessage,
      this.sendMessage,
      this.closeCase,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [caseStatus, customerEmail, refundId, customerMessage],
      channels: [managerDecisions],
    };
  }

  // dex:input field-name:reason value-type:string source:user required:true description:"Reason"
  @rpc({
    lockAttributes: [caseStatus.lock()],
    action: new Action({
      label: "Approve refund",
      requiredPermission: MANAGE_REFUNDS_PERMISSION,
      condition: whenAttributeMatches(caseStatus, AttributeMatch.equalTo(AWAITING_MANAGER)),
    }),
  })
  public approveRefund(context: Context, input: DecisionReason): void {
    const reason = (input.reason ?? "").trim();
    if (reason === "") {
      throw new Error("a reason is required");
    }
    if (caseStatus.get(context) !== AWAITING_MANAGER) {
      throw new RefundNotAwaitingManagerError();
    }
    caseStatus.set(context, "approved");
    managerDecisions.publish(context, { approved: true, reason });
  }

  // dex:input field-name:reason value-type:string source:user required:true description:"Reason"
  @rpc({
    lockAttributes: [caseStatus.lock()],
    action: new Action({
      label: "Reject refund",
      requiredPermission: MANAGE_REFUNDS_PERMISSION,
      condition: whenAttributeMatches(caseStatus, AttributeMatch.equalTo(AWAITING_MANAGER)),
    }),
  })
  public rejectRefund(context: Context, input: DecisionReason): void {
    const reason = (input.reason ?? "").trim();
    if (reason === "") {
      throw new Error("a reason is required");
    }
    if (caseStatus.get(context) !== AWAITING_MANAGER) {
      throw new RefundNotAwaitingManagerError();
    }
    caseStatus.set(context, "rejected");
    managerDecisions.publish(context, { approved: false, reason });
  }
}
