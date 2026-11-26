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

"""RefundDurableTimersFlow: one customer refund that uses every kind of durable Timer."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass, replace
from datetime import timedelta
from enum import StrEnum

from dex import (
    Action,
    AsyncContext,
    Attribute,
    AttributeMatch,
    Channel,
    Context,
    Flow,
    FlowTimeoutHandlerOptions,
    FlowTimeoutPolicy,
    PersistenceSchema,
    RetryPolicy,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Timer,
    Wait,
    force_complete,
    go_to,
    graceful_complete,
    retry_after,
    rpc,
    when_attribute_matches,
)

from dex_examples.products.refund_durable_timers.bank_settlement_service import (
    FakeBankSettlementService,
    SettlementState,
)
from dex_examples.products.refund_durable_timers.notification_service import (
    FakeNotificationService,
)
from dex_examples.products.refund_durable_timers.payment_provider import (
    FakePaymentProvider,
    PaymentRateLimitedError,
)

# Lesson 1: remind the manager every 24 hours.
MANAGER_REMINDER_INTERVAL = timedelta(hours=24)
MANAGER_REMINDER_TIMER_ID = "manager-reminder"
# Lesson 2: close the case after 3 days without a customer message.
CUSTOMER_IDLE_LIMIT = timedelta(days=3)
CUSTOMER_IDLE_TIMER_ID = "customer-idle"
# Lesson 4a: check settlement every 6 hours, at most 12 times.
SETTLEMENT_POLL_INTERVAL = timedelta(hours=6)
SETTLEMENT_POLL_TIMER_ID = "settlement-poll"
MAX_SETTLEMENT_POLLS = 12
# Lesson 4b: check every 10 seconds inside one Step, at most 30 times.
SETTLEMENT_HEARTBEAT_INTERVAL = timedelta(seconds=10)
MAX_SETTLEMENT_HEARTBEAT_CHECKS = 30
SETTLEMENT_HEARTBEAT_LIMIT = timedelta(minutes=10)
# Lesson 5: the whole refund must finish within 7 days.
REFUND_DEADLINE = timedelta(days=7)

MANAGE_REFUNDS_PERMISSION = "refund.manage"
SUPPORT_TEAM_EMAIL = "refund-support@example.com"
MANAGER_EMAIL = "refund-manager@example.com"

AWAITING_DOCUMENTS = "awaiting-documents"
AWAITING_MANAGER = "awaiting-manager"

CASE_STATUS = Attribute("case-status", str, plaintext=True)
REMINDERS_SENT = Attribute("reminders-sent", int, plaintext=True)
LAST_CUSTOMER_MESSAGE = Attribute("last-customer-message", str)
REFUND_ID = Attribute("refund-id", str, plaintext=True)

# Short Steps that change case-status hold its lock, as the RPCs that check it do.
CASE_STATUS_WRITER_OPTIONS = StepOptions(execute_lock_attributes=(CASE_STATUS.lock(),))


class SettlementPolling(StrEnum):
    """How the run waits for the bank: a durable Timer per check, or one heartbeating Step."""

    TIMER = "timer"
    HEARTBEAT = "heartbeat"


@dataclass(frozen=True)
class RefundSimulation:
    """Per-run knobs that make the in-process fakes fail or settle on cue.

    Attributes:
        transient_payment_failures: Payment attempts that fail with a temporary outage first.
        rate_limit_retry_after_seconds: When positive, the next attempt is rate-limited
            once with this Retry-After value.
        settle_after_checks: The settlement check that first reports the refund settled.
        bank_never_settles: The bank reports every check as pending.
        heartbeat_check_interval_seconds: When positive, replaces the 10-second interval
            of the heartbeat settlement Step, so a demo reaches its 30-check limit fast.
    """

    transient_payment_failures: int = 0
    rate_limit_retry_after_seconds: int = 0
    settle_after_checks: int = 1
    bank_never_settles: bool = False
    heartbeat_check_interval_seconds: int = 0


@dataclass(frozen=True)
class RefundRequest:
    customer: str
    customer_email: str
    order_id: str
    amount_cents: int
    settlement_polling: SettlementPolling = SettlementPolling.TIMER
    simulation: RefundSimulation = RefundSimulation()


@dataclass(frozen=True)
class RefundCase:
    request: RefundRequest
    refund_id: str = ""


@dataclass(frozen=True)
class SettlementPoll:
    case: RefundCase
    checks_done: int = 0


@dataclass(frozen=True)
class CustomerMessage:
    text: str
    documents_complete: bool = False


@dataclass(frozen=True)
class ManagerDecision:
    approved: bool
    reason: str


@dataclass(frozen=True)
class DecisionReason:
    reason: str


CUSTOMER_MESSAGES = Channel("customer-messages", CustomerMessage)
MANAGER_DECISIONS = Channel("manager-decisions", ManagerDecision)


# dex:group group-id:documents group-label:"Documents"
# dex:explanation text:"Ask the customer for documents that support the refund."
class RequestSupportingDocuments(Step[RefundRequest]):
    def __init__(self, notifications: FakeNotificationService) -> None:
        self.notifications = notifications

    def get_step_options(self) -> StepOptions:
        return CASE_STATUS_WRITER_OPTIONS

    def execute(self, context: Context, input: RefundRequest) -> StepDecision:
        self.notifications.send(
            input.customer_email,
            f"Please send the documents for your refund of {input.order_id}",
            idempotency_key=f"documents-request-{context.flow_id}",
        )
        CASE_STATUS.set(context, AWAITING_DOCUMENTS)
        return go_to(CollectSupportingDocuments, RefundCase(request=input))


# dex:group group-id:documents group-label:"Documents"
# dex:explanation text:"Wait for customer messages; close the case after 3 idle days."
class CollectSupportingDocuments(Step[RefundCase]):
    def wait_for(self, context: Context, input: RefundCase) -> Wait:
        return Wait.any_of(
            Timer.by_duration(CUSTOMER_IDLE_LIMIT, condition_id=CUSTOMER_IDLE_TIMER_ID),
            CUSTOMER_MESSAGES.for_one(),
        )

    def get_step_options(self) -> StepOptions:
        return CASE_STATUS_WRITER_OPTIONS

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        if context.has_timer_fired():
            return go_to(CloseInactiveCase, input)
        message = CUSTOMER_MESSAGES.results(context)[0]
        LAST_CUSTOMER_MESSAGE.set(context, message.text)
        if message.documents_complete:
            CASE_STATUS.set(context, AWAITING_MANAGER)
            return go_to(AwaitManagerDecision, input)
        return go_to(CollectSupportingDocuments, input)


# dex:group group-id:close group-label:"Close"
# dex:explanation text:"Close the case because the customer stopped replying."
class CloseInactiveCase(Step[RefundCase]):
    def __init__(self, notifications: FakeNotificationService) -> None:
        self.notifications = notifications

    def get_step_options(self) -> StepOptions:
        return CASE_STATUS_WRITER_OPTIONS

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        self.notifications.send(
            input.request.customer_email,
            "We closed your refund request after 3 days without a reply",
            idempotency_key=f"closed-inactive-{context.flow_id}",
        )
        CASE_STATUS.set(context, "closed-inactive")
        return graceful_complete("closed-inactive")


# dex:group group-id:approval group-label:"Approval"
# dex:explanation text:"Wait for the manager's decision; remind the manager every 24 hours."
class AwaitManagerDecision(Step[RefundCase]):
    def __init__(self, notifications: FakeNotificationService) -> None:
        self.notifications = notifications

    def wait_for(self, context: Context, input: RefundCase) -> Wait:
        return Wait.any_of(
            Timer.by_duration(MANAGER_REMINDER_INTERVAL, condition_id=MANAGER_REMINDER_TIMER_ID),
            MANAGER_DECISIONS.for_one(),
        )

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        if context.has_timer_fired():
            self.notifications.send(
                MANAGER_EMAIL,
                f"Reminder: refund {context.flow_id} is waiting for your decision",
                idempotency_key=f"reminder-{context.flow_id}-{context.step_execution_id}",
            )
            REMINDERS_SENT.set(context, (REMINDERS_SENT.get(context) or 0) + 1)
            return go_to(AwaitManagerDecision, input)
        decision = MANAGER_DECISIONS.results(context)[0]
        if decision.approved:
            return go_to(IssueRefund, input)
        return go_to(CloseCase, input)


# dex:group group-id:refund group-label:"Refund"
# dex:explanation text:"Issue the refund through the payment provider, retrying durably."
class IssueRefund(Step[RefundCase]):
    def __init__(self, payment: FakePaymentProvider) -> None:
        self.payment = payment

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=2),
                backoff_coefficient=2.0,
                maximum_interval=timedelta(minutes=5),
                maximum_attempts=10,
                total_duration=timedelta(hours=1),
            )
        )

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        try:
            refund_id = self.payment.issue_refund(
                idempotency_key=f"refund-{context.flow_id}",
                amount_cents=input.request.amount_cents,
                attempt=context.attempt,
                simulation=input.request.simulation,
            )
        except PaymentRateLimitedError as error:
            raise retry_after(error.retry_after_seconds, error) from error
        return go_to(RecordIssuedRefund, replace(input, refund_id=refund_id))


# dex:group group-id:refund group-label:"Refund"
# dex:explanation text:"Record the refund ID and start waiting for settlement."
class RecordIssuedRefund(Step[RefundCase]):
    # Locks last through retry backoff, so IssueRefund leaves status writes to this Step.
    def get_step_options(self) -> StepOptions:
        return CASE_STATUS_WRITER_OPTIONS

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        REFUND_ID.set(context, input.refund_id)
        CASE_STATUS.set(context, "awaiting-settlement")
        if input.request.settlement_polling is SettlementPolling.HEARTBEAT:
            return go_to(AwaitSettlementWithHeartbeat, input)
        return go_to(PollSettlementWithTimer, SettlementPoll(case=input))


# dex:group group-id:settlement group-label:"Settlement"
# dex:explanation text:"Check settlement once per durable 6-hour Timer, up to 12 times."
class PollSettlementWithTimer(Step[SettlementPoll]):
    def __init__(self, bank: FakeBankSettlementService) -> None:
        self.bank = bank

    def wait_for(self, context: Context, input: SettlementPoll) -> Wait:
        if input.checks_done == 0:
            return Wait.skip_immediately()
        return Wait.until(
            Timer.by_duration(SETTLEMENT_POLL_INTERVAL, condition_id=SETTLEMENT_POLL_TIMER_ID)
        )

    def execute(self, context: Context, input: SettlementPoll) -> StepDecision:
        check_number = input.checks_done + 1
        state = self.bank.get_settlement_state(
            input.case.refund_id, check_number, input.case.request.simulation
        )
        if state is SettlementState.SETTLED:
            return go_to(CloseCase, input.case)
        if check_number >= MAX_SETTLEMENT_POLLS:
            return go_to(EscalateUnsettledRefund, input.case)
        return go_to(PollSettlementWithTimer, replace(input, checks_done=check_number))


# dex:group group-id:settlement group-label:"Settlement"
# dex:explanation text:"Check settlement every 10 seconds in one heartbeating Step."
class AwaitSettlementWithHeartbeat(Step[RefundCase]):
    def __init__(self, bank: FakeBankSettlementService) -> None:
        self.bank = bank

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_method_timeout=SETTLEMENT_HEARTBEAT_LIMIT,
            heartbeat_timeout=timedelta(minutes=1),
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=1),
                backoff_coefficient=2.0,
                maximum_attempts=3,
                total_duration=SETTLEMENT_HEARTBEAT_LIMIT,
            ),
        ).on_execute_failure_proceed_to(EscalateUnsettledRefund)

    async def execute(self, context: AsyncContext, input: RefundCase) -> StepDecision:
        interval = input.request.simulation.heartbeat_check_interval_seconds or (
            SETTLEMENT_HEARTBEAT_INTERVAL.total_seconds()
        )
        checks_done = context.get_last_heartbeat_value(int) or 0
        while checks_done < MAX_SETTLEMENT_HEARTBEAT_CHECKS:
            checks_done += 1
            state = self.bank.get_settlement_state(
                input.refund_id, checks_done, input.request.simulation
            )
            if state is SettlementState.SETTLED:
                return go_to(CloseCase, input)
            await context.heartbeat(checks_done)
            await asyncio.sleep(interval)
        return go_to(EscalateUnsettledRefund, input)


# dex:group group-id:close group-label:"Close"
# dex:explanation text:"Hand an unsettled refund to the support team."
class EscalateUnsettledRefund(Step[RefundCase]):
    def __init__(self, notifications: FakeNotificationService) -> None:
        self.notifications = notifications

    def get_step_options(self) -> StepOptions:
        return CASE_STATUS_WRITER_OPTIONS

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        self.notifications.send(
            SUPPORT_TEAM_EMAIL,
            f"Refund {input.refund_id} for {context.flow_id} has not settled; please follow up",
            idempotency_key=f"escalate-settlement-{context.flow_id}",
        )
        CASE_STATUS.set(context, "escalated-unsettled")
        return graceful_complete("escalated-unsettled")


# dex:group group-id:close group-label:"Close"
# dex:explanation text:"Tell the customer the outcome and close the case."
class CloseCase(Step[RefundCase]):
    def __init__(self, notifications: FakeNotificationService) -> None:
        self.notifications = notifications

    def get_step_options(self) -> StepOptions:
        return CASE_STATUS_WRITER_OPTIONS

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        outcome = "refunded" if input.refund_id else "rejected"
        self.notifications.send(
            input.request.customer_email,
            f"Your refund request for {input.request.order_id} was {outcome}",
            idempotency_key=f"close-{context.flow_id}",
        )
        CASE_STATUS.set(context, f"closed-{outcome}")
        return graceful_complete(outcome)


class RefundNotAwaitingManagerError(ValueError):
    """Raised when a manager decides a refund that is not waiting for a decision."""


class RefundNotAwaitingDocumentsError(ValueError):
    """Raised when a customer message arrives after document collection ended."""


class RefundDurableTimersFlow(Flow[RefundRequest]):
    def __init__(
        self,
        payment: FakePaymentProvider,
        bank: FakeBankSettlementService,
        notifications: FakeNotificationService,
    ) -> None:
        self.notifications = notifications
        self.request_supporting_documents = RequestSupportingDocuments(notifications)
        self.collect_supporting_documents = CollectSupportingDocuments()
        self.close_inactive_case = CloseInactiveCase(notifications)
        self.await_manager_decision = AwaitManagerDecision(notifications)
        self.issue_refund = IssueRefund(payment)
        self.record_issued_refund = RecordIssuedRefund()
        self.poll_settlement_with_timer = PollSettlementWithTimer(bank)
        self.await_settlement_with_heartbeat = AwaitSettlementWithHeartbeat(bank)
        self.escalate_unsettled_refund = EscalateUnsettledRefund(notifications)
        self.close_case = CloseCase(notifications)

    def get_steps(self) -> StepList[RefundRequest]:
        return StepList.start_step(self.request_supporting_documents).other_steps(
            self.collect_supporting_documents,
            self.close_inactive_case,
            self.await_manager_decision,
            self.issue_refund,
            self.record_issued_refund,
            self.poll_settlement_with_timer,
            self.await_settlement_with_heartbeat,
            self.escalate_unsettled_refund,
            self.close_case,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            CASE_STATUS,
            REMINDERS_SENT,
            LAST_CUSTOMER_MESSAGE,
            REFUND_ID,
            CUSTOMER_MESSAGES,
            MANAGER_DECISIONS,
        )

    # Dex calls this when the 7-day Flow timeout fires; any waiting Step ends.
    def handle_timeout(self, context: Context) -> StepDecision:
        self.notifications.send(
            SUPPORT_TEAM_EMAIL,
            f"Refund {context.flow_id} missed its 7-day deadline; please take it over",
            idempotency_key=f"escalate-deadline-{context.flow_id}",
        )
        CASE_STATUS.set(context, "escalated-deadline")
        return force_complete("escalated-deadline")

    @rpc(lock_attributes=(CASE_STATUS.lock(),))
    def send_customer_message(self, context: Context, input: CustomerMessage) -> None:
        if not input.text.strip():
            raise ValueError("a message text is required")
        if CASE_STATUS.get(context) != AWAITING_DOCUMENTS:
            raise RefundNotAwaitingDocumentsError("this refund is not collecting documents")
        CUSTOMER_MESSAGES.publish(context, input)

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
        self._decide(context, ManagerDecision(approved=True, reason=input.reason))

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
        self._decide(context, ManagerDecision(approved=False, reason=input.reason))

    def _decide(self, context: Context, decision: ManagerDecision) -> None:
        if not decision.reason.strip():
            raise ValueError("a reason is required")
        if CASE_STATUS.get(context) != AWAITING_MANAGER:
            raise RefundNotAwaitingManagerError("this refund is not waiting for a manager")
        CASE_STATUS.set(context, "approved" if decision.approved else "rejected")
        MANAGER_DECISIONS.publish(context, decision)


def refund_start_options() -> StartFlowOptions:
    """Returns the start options every refund uses: a 7-day deadline handled by handle_timeout."""
    return StartFlowOptions(
        timeout=REFUND_DEADLINE,
        timeout_policy=FlowTimeoutPolicy.HANDLER,
        timeout_handler_options=FlowTimeoutHandlerOptions(
            method_timeout=timedelta(minutes=1),
            lock_attributes=(CASE_STATUS.lock(),),
            retry=RetryPolicy(
                initial_interval=timedelta(seconds=5),
                backoff_coefficient=2.0,
                maximum_interval=timedelta(minutes=1),
                maximum_attempts=5,
            ),
        ),
    )
