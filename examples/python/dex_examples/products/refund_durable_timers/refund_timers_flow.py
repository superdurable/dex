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
    StepMovement,
    StepOptions,
    Timer,
    Wait,
    dead_end,
    force_complete,
    go_to,
    go_to_many,
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
DEADLINE_HANDLING_TIMEOUT = timedelta(minutes=1)
DEADLINE_HANDLING_RETRY = RetryPolicy(
    initial_interval=timedelta(seconds=5),
    backoff_coefficient=2.0,
    maximum_interval=timedelta(minutes=1),
    maximum_attempts=5,
)

MANAGE_REFUNDS_PERMISSION = "refund.manage"
SUPPORT_TEAM_EMAIL = "refund-support@example.com"
MANAGER_EMAIL = "refund-manager@example.com"

AWAITING_DOCUMENTS = "awaiting-documents"
AWAITING_MANAGER = "awaiting-manager"
CLOSING_INACTIVE = "closing-inactive"
ESCALATED_DEADLINE = "escalated-deadline"
# A case-status with one of these prefixes is a final decision.
FINAL_STATUS_PREFIXES = ("closed-", "escalated-")

CASE_STATUS = Attribute("case-status", str, plaintext=True)
REMINDERS_SENT = Attribute("reminders-sent", int, plaintext=True)
LAST_CUSTOMER_MESSAGE = Attribute("last-customer-message", str)
REFUND_ID = Attribute("refund-id", str, plaintext=True)

# Short Steps that change case-status hold its lock, as the RPCs that check it do.
# They never call an external system: the lock lasts through retries and backoff.
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


@dataclass(frozen=True)
class ClosingNotification:
    """A committed closing decision whose email is still to be sent.

    Attributes:
        recipient: The email address to notify.
        subject: The email subject.
        idempotency_key: Identifies the decision, so a retried send emails once.
        outcome: The Flow output after the email is sent.
        cancel_other_steps: Force-completes the Flow, cancelling Steps that still wait.
    """

    recipient: str
    subject: str
    idempotency_key: str
    outcome: str
    cancel_other_steps: bool = False


def is_deadline_escalated(context: Context) -> bool:
    """Returns whether the timeout handler took the case over; later status writers then end."""
    return CASE_STATUS.get(context) == ESCALATED_DEADLINE


CUSTOMER_MESSAGES = Channel("customer-messages", CustomerMessage)
MANAGER_DECISIONS = Channel("manager-decisions", ManagerDecision)


# dex:group group-id:documents group-label:"Documents"
# dex:explanation text:"Open the documents stage so the customer can send messages."
class RequestSupportingDocuments(Step[RefundRequest]):
    def get_step_options(self) -> StepOptions:
        return CASE_STATUS_WRITER_OPTIONS

    def execute(self, context: Context, input: RefundRequest) -> StepDecision:
        if is_deadline_escalated(context):
            return dead_end()
        CASE_STATUS.set(context, AWAITING_DOCUMENTS)
        return go_to(SendDocumentsRequest, RefundCase(request=input))


# dex:group group-id:documents group-label:"Documents"
# dex:explanation text:"Email the customer to ask for documents that support the refund."
class SendDocumentsRequest(Step[RefundCase]):
    def __init__(self, notifications: FakeNotificationService) -> None:
        self.notifications = notifications

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        # The deadline cancels this Step; skip the email when that already happened.
        if is_deadline_escalated(context) or context.is_cancellation_requested():
            return dead_end()
        self.notifications.send(
            input.request.customer_email,
            f"Please send the documents for your refund of {input.request.order_id}",
            idempotency_key=f"documents-request-{context.flow_id}",
        )
        return go_to(CollectSupportingDocuments, input)


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
        if is_deadline_escalated(context):
            return dead_end()
        if context.has_timer_fired():
            # A message accepted after the Timer fired restarts the wait instead.
            if CUSTOMER_MESSAGES.size(context) > 0:
                return go_to(CollectSupportingDocuments, input)
            # The RPC stage check refuses messages from here on.
            CASE_STATUS.set(context, CLOSING_INACTIVE)
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
    def get_step_options(self) -> StepOptions:
        return CASE_STATUS_WRITER_OPTIONS

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        if is_deadline_escalated(context):
            return dead_end()
        CASE_STATUS.set(context, "closed-inactive")
        return go_to(
            SendClosingNotification,
            ClosingNotification(
                recipient=input.request.customer_email,
                subject="We closed your refund request after 3 days without a reply",
                idempotency_key=f"closed-inactive-{context.flow_id}",
                outcome="closed-inactive",
            ),
        )


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
        if is_deadline_escalated(context):
            return dead_end()
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
    def get_step_options(self) -> StepOptions:
        return CASE_STATUS_WRITER_OPTIONS

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        if is_deadline_escalated(context):
            return dead_end()
        CASE_STATUS.set(context, "escalated-unsettled")
        return go_to(
            SendClosingNotification,
            ClosingNotification(
                recipient=SUPPORT_TEAM_EMAIL,
                subject=(
                    f"Refund {input.refund_id} for {context.flow_id} has not settled;"
                    " please follow up"
                ),
                idempotency_key=f"escalate-settlement-{context.flow_id}",
                outcome="escalated-unsettled",
            ),
        )


# dex:group group-id:close group-label:"Close"
# dex:explanation text:"Record the refund outcome and close the case."
class CloseCase(Step[RefundCase]):
    def get_step_options(self) -> StepOptions:
        return CASE_STATUS_WRITER_OPTIONS

    def execute(self, context: Context, input: RefundCase) -> StepDecision:
        if is_deadline_escalated(context):
            return dead_end()
        outcome = "refunded" if input.refund_id else "rejected"
        CASE_STATUS.set(context, f"closed-{outcome}")
        return go_to(
            SendClosingNotification,
            ClosingNotification(
                recipient=input.request.customer_email,
                subject=f"Your refund request for {input.request.order_id} was {outcome}",
                idempotency_key=f"close-{outcome}-{context.flow_id}",
                outcome=outcome,
            ),
        )


# dex:group group-id:close group-label:"Close"
# dex:explanation text:"Email the committed closing decision and complete the Flow."
class SendClosingNotification(Step[ClosingNotification]):
    # Takes no lock, so its retries never block the RPCs.
    def __init__(self, notifications: FakeNotificationService) -> None:
        self.notifications = notifications

    def execute(self, context: Context, input: ClosingNotification) -> StepDecision:
        self.notifications.send(
            input.recipient, input.subject, idempotency_key=input.idempotency_key
        )
        if input.cancel_other_steps:
            return force_complete(input.outcome)
        return graceful_complete(input.outcome)


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
        self.request_supporting_documents = RequestSupportingDocuments()
        self.send_documents_request = SendDocumentsRequest(notifications)
        self.collect_supporting_documents = CollectSupportingDocuments()
        self.close_inactive_case = CloseInactiveCase()
        self.await_manager_decision = AwaitManagerDecision(notifications)
        self.issue_refund = IssueRefund(payment)
        self.record_issued_refund = RecordIssuedRefund()
        self.poll_settlement_with_timer = PollSettlementWithTimer(bank)
        self.await_settlement_with_heartbeat = AwaitSettlementWithHeartbeat(bank)
        self.escalate_unsettled_refund = EscalateUnsettledRefund()
        self.close_case = CloseCase()
        self.send_closing_notification = SendClosingNotification(notifications)

    def get_steps(self) -> StepList[RefundRequest]:
        return StepList.start_step(self.request_supporting_documents).other_steps(
            self.send_documents_request,
            self.collect_supporting_documents,
            self.close_inactive_case,
            self.await_manager_decision,
            self.issue_refund,
            self.record_issued_refund,
            self.poll_settlement_with_timer,
            self.await_settlement_with_heartbeat,
            self.escalate_unsettled_refund,
            self.close_case,
            self.send_closing_notification,
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

    # Dex calls this when the 7-day Flow timeout fires; other Steps keep running
    # until SendClosingNotification force-completes the Flow.
    def handle_timeout(self, context: Context) -> StepDecision:
        status = CASE_STATUS.get(context) or ""
        # A final decision is committed; its SendClosingNotification completes the Flow.
        if status.startswith(FINAL_STATUS_PREFIXES):
            return dead_end()
        CASE_STATUS.set(context, ESCALATED_DEADLINE)
        notification = ClosingNotification(
            recipient=SUPPORT_TEAM_EMAIL,
            subject=f"Refund {context.flow_id} missed its 7-day deadline; please take it over",
            idempotency_key=f"escalate-deadline-{context.flow_id}",
            outcome=ESCALATED_DEADLINE,
            cancel_other_steps=True,
        )
        # The deadline email keeps the handler's short timeout and retries.
        email_options = StepOptions(
            execute_method_timeout=DEADLINE_HANDLING_TIMEOUT,
            execute_retry=DEADLINE_HANDLING_RETRY,
        )
        return go_to_many(
            StepMovement.of(SendClosingNotification, notification, options=email_options)
        ).with_canceling_steps(RequestSupportingDocuments, SendDocumentsRequest)

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
            method_timeout=DEADLINE_HANDLING_TIMEOUT,
            lock_attributes=(CASE_STATUS.lock(),),
            retry=DEADLINE_HANDLING_RETRY,
        ),
    )
