// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//! CustomerRefundFlow: check a refund request, ask a manager when needed, refund, and tell the customer.

use std::sync::{Arc, LazyLock};
use std::time::Duration;

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, RetryPolicy,
    Rpc, RpcList, Step, StepDecision, StepList, StepOptions, Wait,
};
use serde::{Deserialize, Serialize};

use super::billing_service::{BillingError, FakeBillingService, Order};
use super::email_service::FakeEmailService;
use super::message_model::MockMessageModel;

pub const REFUND_WINDOW_DAYS: u32 = 30;
pub const MANAGER_APPROVAL_LIMIT_CENTS: u64 = 100_000;

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum CaseStatus {
    Received,
    AwaitingManager,
    Approved,
    Rejected,
    Denied,
    Refunded,
    ClosedRefunded,
    ClosedDenied,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum RefundOutcome {
    Refunded,
    Denied,
}

impl RefundOutcome {
    fn closed_status(self) -> CaseStatus {
        match self {
            Self::Refunded => CaseStatus::ClosedRefunded,
            Self::Denied => CaseStatus::ClosedDenied,
        }
    }
}

pub static CASE_STATUS: LazyLock<Attribute<CaseStatus>> =
    LazyLock::new(|| Attribute::new("case-status"));
pub static CUSTOMER_EMAIL: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("customer-email"));
pub static REFUND_ID: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("refund-id"));
pub static CUSTOMER_MESSAGE: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("customer-message"));
pub static MANAGER_DECISIONS: LazyLock<Channel<ManagerDecision>> =
    LazyLock::new(|| Channel::new("manager-decisions"));

pub const APPROVE_REFUND: Rpc<DecisionReason, ()> = Rpc::new("ApproveRefund");
pub const REJECT_REFUND: Rpc<DecisionReason, ()> = Rpc::new("RejectRefund");

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct RefundRequest {
    pub customer: String,
    pub customer_email: String,
    pub order_id: String,
    pub reason: String,
    #[serde(default)]
    pub payment_timeouts_to_simulate: u32,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ManagerDecision {
    pub approved: bool,
    pub reason: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct DecisionReason {
    pub reason: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct RefundCase {
    pub request: RefundRequest,
    pub order: Order,
}

/// A case with its outcome, carried through the closing Steps.
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ResolvedCase {
    pub case: RefundCase,
    pub outcome: RefundOutcome,
    pub note: String,
    pub refund_id: String,
    pub message: String,
}

impl ResolvedCase {
    fn new(case: RefundCase, outcome: RefundOutcome, note: impl Into<String>) -> Self {
        Self {
            case,
            outcome,
            note: note.into(),
            refund_id: String::new(),
            message: String::new(),
        }
    }
}

/// The external services that the Steps call.
pub struct RefundServices {
    pub billing: FakeBillingService,
    pub email: FakeEmailService,
    pub model: MockMessageModel,
}

/// Intake: record the refund request and the customer's email address.
#[derive(Clone)]
pub struct ReceiveRequest {
    services: Arc<RefundServices>,
}

impl Step for ReceiveRequest {
    type Input = RefundRequest;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        CASE_STATUS.set(context, CaseStatus::Received)?;
        CUSTOMER_EMAIL.set(context, input.customer_email.clone())?;
        Ok(StepDecision::go_to(&CheckOrder::new(&self.services), input))
    }
}

/// Evidence: look up the order in billing.
#[derive(Clone)]
pub struct CheckOrder {
    services: Arc<RefundServices>,
}

impl CheckOrder {
    fn new(services: &Arc<RefundServices>) -> Self {
        Self {
            services: Arc::clone(services),
        }
    }
}

impl Step for CheckOrder {
    type Input = RefundRequest;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let order = self
            .services
            .billing
            .get_order(&input.order_id)
            .map_err(billing_failure)?;
        let case = RefundCase {
            request: input,
            order,
        };
        Ok(StepDecision::go_to(&CheckPolicy::new(&self.services), case))
    }
}

/// Evidence: refund, deny, or ask a manager, by the refund policy.
#[derive(Clone)]
pub struct CheckPolicy {
    services: Arc<RefundServices>,
}

impl CheckPolicy {
    fn new(services: &Arc<RefundServices>) -> Self {
        Self {
            services: Arc::clone(services),
        }
    }
}

impl Step for CheckPolicy {
    type Input = RefundCase;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let order = &input.order;
        let mut denial_reasons = Vec::new();
        if order.days_since_charge > REFUND_WINDOW_DAYS {
            denial_reasons.push("The charge is outside the 30-day refund window.");
        }
        if order.outage_hours == 0 {
            denial_reasons.push("No service outage was recorded for this charge.");
        }
        if !denial_reasons.is_empty() {
            CASE_STATUS.set(context, CaseStatus::Denied)?;
            let note = denial_reasons.join(" ");
            let denied = ResolvedCase::new(input, RefundOutcome::Denied, note);
            return Ok(StepDecision::go_to(
                &WriteCustomerMessage::new(&self.services),
                denied,
            ));
        }
        if order.amount_cents > MANAGER_APPROVAL_LIMIT_CENTS {
            CASE_STATUS.set(context, CaseStatus::AwaitingManager)?;
            return Ok(StepDecision::go_to(
                &RequestManagerApproval::new(&self.services),
                input,
            ));
        }
        Ok(StepDecision::go_to(
            &IssueRefund::new(&self.services),
            input,
        ))
    }
}

/// Control: wait for a manager to approve or reject the refund.
#[derive(Clone)]
pub struct RequestManagerApproval {
    services: Arc<RefundServices>,
}

impl RequestManagerApproval {
    fn new(services: &Arc<RefundServices>) -> Self {
        Self {
            services: Arc::clone(services),
        }
    }
}

impl Step for RequestManagerApproval {
    type Input = RefundCase;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(MANAGER_DECISIONS.for_one()))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let decision = MANAGER_DECISIONS
            .condition_results(context)?
            .into_iter()
            .next()
            .ok_or_else(|| HandlerError::new("MissingManagerDecision", "no manager decision"))?;
        if decision.approved {
            return Ok(StepDecision::go_to(
                &IssueRefund::new(&self.services),
                input,
            ));
        }
        CASE_STATUS.set(context, CaseStatus::Denied)?;
        let denied = ResolvedCase::new(input, RefundOutcome::Denied, decision.reason);
        Ok(StepDecision::go_to(
            &WriteCustomerMessage::new(&self.services),
            denied,
        ))
    }
}

/// Resolution: refund the order through billing, once per case.
#[derive(Clone)]
pub struct IssueRefund {
    services: Arc<RefundServices>,
}

impl IssueRefund {
    fn new(services: &Arc<RefundServices>) -> Self {
        Self {
            services: Arc::clone(services),
        }
    }
}

impl Step for IssueRefund {
    type Input = RefundCase;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let simulate_timeout = context.attempt() <= input.request.payment_timeouts_to_simulate;
        let idempotency_key = format!("refund-{}", context.flow_id());
        let refund_id = self
            .services
            .billing
            .refund(&input.order, &idempotency_key, simulate_timeout)
            .map_err(billing_failure)?;
        REFUND_ID.set(context, refund_id.clone())?;
        CASE_STATUS.set(context, CaseStatus::Refunded)?;
        let mut refunded = ResolvedCase::new(input, RefundOutcome::Refunded, "");
        refunded.refund_id = refund_id;
        Ok(StepDecision::go_to(
            &WriteCustomerMessage::new(&self.services),
            refunded,
        ))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        let retry = RetryPolicy::new()
            .initial_interval(Duration::from_secs(5))
            .backoff_coefficient(1.0)
            .maximum_attempts(10);
        StepOptions::new().execute_retry(retry)
    }
}

/// Close: ask the model to write the customer message.
#[derive(Clone)]
pub struct WriteCustomerMessage {
    services: Arc<RefundServices>,
}

impl WriteCustomerMessage {
    fn new(services: &Arc<RefundServices>) -> Self {
        Self {
            services: Arc::clone(services),
        }
    }
}

impl Step for WriteCustomerMessage {
    type Input = ResolvedCase;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let message = self.services.model.write_message(
            &input.case.request.customer,
            input.outcome,
            input.case.order.amount_cents,
            &input.note,
        );
        CUSTOMER_MESSAGE.set(context, message.clone())?;
        let written = ResolvedCase { message, ..input };
        Ok(StepDecision::go_to(
            &SendMessage::new(&self.services),
            written,
        ))
    }
}

/// Close: email the message to the customer, once per case.
#[derive(Clone)]
pub struct SendMessage {
    services: Arc<RefundServices>,
}

impl SendMessage {
    fn new(services: &Arc<RefundServices>) -> Self {
        Self {
            services: Arc::clone(services),
        }
    }
}

impl Step for SendMessage {
    type Input = ResolvedCase;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        self.services
            .email
            .send(
                &input.case.request.customer_email,
                &input.message,
                &format!("email-{}", context.flow_id()),
            )
            .map_err(HandlerError::from_error)?;
        Ok(StepDecision::go_to(&CloseCase, input))
    }
}

/// Close: close the case with its outcome.
#[derive(Clone, Default)]
pub struct CloseCase;

impl Step for CloseCase {
    type Input = ResolvedCase;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        CASE_STATUS.set(context, input.outcome.closed_status())?;
        Ok(StepDecision::graceful_complete(input.outcome))
    }
}

#[derive(Clone)]
pub struct CustomerRefundFlow {
    receive_request: ReceiveRequest,
    check_order: CheckOrder,
    check_policy: CheckPolicy,
    request_manager_approval: RequestManagerApproval,
    issue_refund: IssueRefund,
    write_customer_message: WriteCustomerMessage,
    send_message: SendMessage,
    close_case: CloseCase,
}

impl CustomerRefundFlow {
    pub fn new(services: Arc<RefundServices>) -> Self {
        Self {
            receive_request: ReceiveRequest {
                services: Arc::clone(&services),
            },
            check_order: CheckOrder::new(&services),
            check_policy: CheckPolicy::new(&services),
            request_manager_approval: RequestManagerApproval::new(&services),
            issue_refund: IssueRefund::new(&services),
            write_customer_message: WriteCustomerMessage::new(&services),
            send_message: SendMessage::new(&services),
            close_case: CloseCase,
        }
    }

    /// Returns the Step that the shared server's start route waits for.
    pub(crate) fn check_policy(&self) -> &CheckPolicy {
        &self.check_policy
    }

    fn approve_refund(&self, context: &mut Context, input: DecisionReason) -> HandlerResult<()> {
        record_manager_decision(context, true, input.reason)
    }

    fn reject_refund(&self, context: &mut Context, input: DecisionReason) -> HandlerResult<()> {
        record_manager_decision(context, false, input.reason)
    }
}

impl Flow for CustomerRefundFlow {
    type StartInput = RefundRequest;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.receive_request)
            .and(&self.check_order)
            .and(&self.check_policy)
            .and(&self.request_manager_approval)
            .and(&self.issue_refund)
            .and(&self.write_customer_message)
            .and(&self.send_message)
            .and(&self.close_case)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&CASE_STATUS)
            .attribute(&CUSTOMER_EMAIL)
            .attribute(&REFUND_ID)
            .attribute(&CUSTOMER_MESSAGE)
            .channel(&MANAGER_DECISIONS)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(
                APPROVE_REFUND.lock(CASE_STATUS.lock()),
                Self::approve_refund,
            )
            .procedure(REJECT_REFUND.lock(CASE_STATUS.lock()), Self::reject_refund)
    }
}

fn record_manager_decision(
    context: &mut Context,
    approved: bool,
    reason: String,
) -> HandlerResult<()> {
    let reason = reason.trim().to_string();
    if reason.is_empty() {
        return Err(HandlerError::new(
            "BlankDecisionReason",
            "a manager decision needs a reason",
        ));
    }
    if CASE_STATUS.get(context)? != Some(CaseStatus::AwaitingManager) {
        return Err(HandlerError::new(
            "RefundNotAwaitingManager",
            "this refund is not waiting for a manager",
        ));
    }
    let status = if approved {
        CaseStatus::Approved
    } else {
        CaseStatus::Rejected
    };
    CASE_STATUS.set(context, status)?;
    MANAGER_DECISIONS.publish(context, ManagerDecision { approved, reason })
}

fn billing_failure(error: BillingError) -> HandlerError {
    HandlerError::new(error.error_type(), error.to_string())
}
