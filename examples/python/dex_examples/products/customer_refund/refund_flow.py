# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""CustomerRefundFlow: check a refund request, ask a manager when needed, refund, and tell the customer."""

from __future__ import annotations

from dataclasses import dataclass, replace
from datetime import timedelta

from dex import (
    Action,
    Attribute,
    AttributeMatch,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Wait,
    go_to,
    graceful_complete,
    rpc,
    when_attribute_matches,
)

from dex_examples.products.customer_refund.billing_service import FakeBillingService, Order
from dex_examples.products.customer_refund.email_service import FakeEmailService
from dex_examples.products.customer_refund.message_model import MockMessageModel

REFUND_WINDOW_DAYS = 30
MANAGER_APPROVAL_LIMIT_USD = 1000.00
AWAITING_MANAGER = "awaiting-manager"
MANAGE_REFUNDS_PERMISSION = "refund.manage"

# Readable without the payload key, so Dex Web can show and match them.
CASE_STATUS = Attribute("case-status", str, plaintext=True)
CUSTOMER_EMAIL = Attribute("customer-email", str, plaintext=True)
# Encrypted when the Worker has a payload key.
REFUND_ID = Attribute("refund-id", str)
CUSTOMER_MESSAGE = Attribute("customer-message", str)


@dataclass(frozen=True)
class RefundRequest:
    customer: str
    customer_email: str
    order_id: str
    reason: str
    payment_timeouts_to_simulate: int = 0


@dataclass(frozen=True)
class ManagerDecision:
    approved: bool
    reason: str


@dataclass(frozen=True)
class DecisionReason:
    reason: str


@dataclass(frozen=True)
class RefundCase:
    request: RefundRequest
    order: Order
    outcome: str = ""
    note: str = ""
    refund_id: str = ""
    message: str = ""


MANAGER_DECISIONS = Channel("manager-decisions", ManagerDecision)


# dex:group group-id:intake group-label:"Intake"
# dex:explanation text:"Record the refund request and the customer's email address."
class ReceiveRequest(Step[RefundRequest]):
    def execute(self, context: Context, input: RefundRequest) -> StepDecision:
        CASE_STATUS.set(context, "received")
        CUSTOMER_EMAIL.set(context, input.customer_email)
        return go_to(CheckOrder, input)


# dex:group group-id:evidence group-label:"Evidence"
# dex:explanation text:"Look up the order in billing."
class CheckOrder(Step[RefundRequest]):
    def __init__(self, billing: FakeBillingService) -> None:
        self.billing = billing

    def execute(self, context: Context, input: RefundRequest) -> StepDecision:
        order = self.billing.get_order(input.order_id)
        return go_to(CheckPolicy, RefundCase(request=input, order=order))


# dex:group group-id:evidence group-label:"Evidence"
# dex:explanation text:"Refund, deny, or ask a manager, by the refund policy."
class CheckPolicy(Step[RefundCase]):
    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        order = input.order
        if order.days_since_charge > REFUND_WINDOW_DAYS:
            CASE_STATUS.set(context, "denied")
            note = "The charge is older than our 30-day refund window."
            return go_to(WriteCustomerMessage, replace(input, outcome="denied", note=note))
        if order.outage_hours == 0:
            CASE_STATUS.set(context, "denied")
            note = "We found no service interruption on this order."
            return go_to(WriteCustomerMessage, replace(input, outcome="denied", note=note))
        if order.amount_usd > MANAGER_APPROVAL_LIMIT_USD:
            CASE_STATUS.set(context, AWAITING_MANAGER)
            return go_to(RequestManagerApproval, input)
        return go_to(IssueRefund, input)


# dex:group group-id:control group-label:"Control"
# dex:explanation text:"Wait for a manager to approve or reject the refund."
class RequestManagerApproval(Step[RefundCase]):
    def wait_for(self, context: Context, input: RefundCase) -> Wait:
        return Wait.until(MANAGER_DECISIONS.for_one())

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        decision = MANAGER_DECISIONS.results(context)[0]
        if decision.approved:
            return go_to(IssueRefund, input)
        CASE_STATUS.set(context, "denied")
        return go_to(WriteCustomerMessage, replace(input, outcome="denied", note=decision.reason))


# dex:group group-id:resolution group-label:"Resolution"
# dex:explanation text:"Refund the order through billing, once per case."
class IssueRefund(Step[RefundCase]):
    def __init__(self, billing: FakeBillingService) -> None:
        self.billing = billing

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        simulate_timeout = context.attempt <= input.request.payment_timeouts_to_simulate
        refund_id = self.billing.refund(
            input.order,
            idempotency_key=f"refund-{context.flow_id}",
            simulate_timeout=simulate_timeout,
        )
        REFUND_ID.set(context, refund_id)
        CASE_STATUS.set(context, "refunded")
        return go_to(WriteCustomerMessage, replace(input, outcome="refunded", refund_id=refund_id))

    def get_step_options(self) -> StepOptions:
        retry = RetryPolicy(
            initial_interval=timedelta(seconds=5),
            backoff_coefficient=1.0,
            maximum_attempts=10,
        )
        return StepOptions(execute_retry=retry)


# dex:group group-id:close group-label:"Close"
# dex:explanation text:"Ask the model to write the customer message."
class WriteCustomerMessage(Step[RefundCase]):
    def __init__(self, model: MockMessageModel) -> None:
        self.model = model

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        message = self.model.write_message(
            input.request.customer, input.outcome, input.order.amount_usd, input.note
        )
        CUSTOMER_MESSAGE.set(context, message)
        return go_to(SendMessage, replace(input, message=message))


# dex:group group-id:close group-label:"Close"
# dex:explanation text:"Email the message to the customer, once per case."
class SendMessage(Step[RefundCase]):
    def __init__(self, email: FakeEmailService) -> None:
        self.email = email

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        self.email.send(
            input.request.customer_email,
            input.message,
            idempotency_key=f"email-{context.flow_id}",
        )
        return go_to(CloseCase, input)


# dex:group group-id:close group-label:"Close"
# dex:explanation text:"Close the case with its outcome."
class CloseCase(Step[RefundCase]):
    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        CASE_STATUS.set(context, f"closed-{input.outcome}")
        return graceful_complete(input.outcome)


class CustomerRefundFlow(Flow[RefundRequest]):
    def __init__(
        self, billing: FakeBillingService, email: FakeEmailService, model: MockMessageModel
    ) -> None:
        self.receive_request = ReceiveRequest()
        self.check_order = CheckOrder(billing)
        self.check_policy = CheckPolicy()
        self.request_manager_approval = RequestManagerApproval()
        self.issue_refund = IssueRefund(billing)
        self.write_customer_message = WriteCustomerMessage(model)
        self.send_message = SendMessage(email)
        self.close_case = CloseCase()

    def get_steps(self) -> StepList[RefundRequest]:
        return StepList.start_step(self.receive_request).other_steps(
            self.check_order,
            self.check_policy,
            self.request_manager_approval,
            self.issue_refund,
            self.write_customer_message,
            self.send_message,
            self.close_case,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            CASE_STATUS, CUSTOMER_EMAIL, REFUND_ID, CUSTOMER_MESSAGE, MANAGER_DECISIONS
        )

    # dex:input field-name:reason value-type:string source:user required:true description:"Reason"
    @rpc(
        lock_attributes=(CASE_STATUS.lock(),),
        action=Action(
            "Approve refund",
            required_permission=MANAGE_REFUNDS_PERMISSION,
            condition=when_attribute_matches(
                CASE_STATUS, AttributeMatch.equal_to(AWAITING_MANAGER)
            ),
        ),
    )
    def approve_refund(self, context: Context, input: DecisionReason) -> None:
        if not input.reason.strip():
            raise ValueError("a reason is required")
        if CASE_STATUS.get(context) != AWAITING_MANAGER:
            raise RefundNotAwaitingManagerError("this refund is not waiting for a manager")
        CASE_STATUS.set(context, "approved")
        MANAGER_DECISIONS.publish(context, ManagerDecision(approved=True, reason=input.reason))

    # dex:input field-name:reason value-type:string source:user required:true description:"Reason"
    @rpc(
        lock_attributes=(CASE_STATUS.lock(),),
        action=Action(
            "Reject refund",
            required_permission=MANAGE_REFUNDS_PERMISSION,
            condition=when_attribute_matches(
                CASE_STATUS, AttributeMatch.equal_to(AWAITING_MANAGER)
            ),
        ),
    )
    def reject_refund(self, context: Context, input: DecisionReason) -> None:
        if not input.reason.strip():
            raise ValueError("a reason is required")
        if CASE_STATUS.get(context) != AWAITING_MANAGER:
            raise RefundNotAwaitingManagerError("this refund is not waiting for a manager")
        CASE_STATUS.set(context, "rejected")
        MANAGER_DECISIONS.publish(context, ManagerDecision(approved=False, reason=input.reason))


class RefundNotAwaitingManagerError(ValueError):
    pass


REFUND_NOT_AWAITING_MANAGER_ERROR_TYPE = (
    f"{RefundNotAwaitingManagerError.__module__}.{RefundNotAwaitingManagerError.__qualname__}"
)
