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

use std::sync::{Arc, LazyLock};
use std::time::Duration;

use dex_sdk::{
    Attribute, Channel, Client, Context, Flow, FlowStatus, HandlerError, HandlerResult,
    PersistenceSchema, Rpc, RpcList, SdkError, Step, StepDecision, StepList, StopFlowOptions,
    SubFlow, SubFlowOptions, SubFlowReusePolicy, Wait,
};
use serde::{Deserialize, Serialize};

use super::credit_check::{CreditCheckFlow, CreditCheckRequest};

/// The child's own Flow timeout. It stops the child even when the parent
/// closes without stopping it.
pub const CREDIT_CHECK_TIMEOUT: Duration = Duration::from_secs(7 * 24 * 60 * 60);

const APPLICATION_CANCELED_PREFIX: &str = "application canceled: ";

/// The input of `LoanApplicationFlow`.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LoanApplication {
    pub applicant_id: String,
    pub credit_report_delay_seconds: u64,
}

pub static STOPPED_CREDIT_CHECK_FLOW_ID: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("stoppedCreditCheckFlowId"));
pub static APPLICATION_CANCELED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("applicationCanceled"));
pub static UNDERWRITER_DECIDED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("underwriterDecided"));

pub const CANCEL_APPLICATION: Rpc<String, ()> = Rpc::new("cancel_application");
pub const DECIDE_APPLICATION: Rpc<String, ()> = Rpc::new("decide_application");

/// Runs a credit check in a child Flow. Canceling the application also stops
/// the child Flow.
#[derive(Default)]
pub struct LoanApplicationFlow {
    run_credit_check: RunCreditCheck,
    await_underwriter_decision: AwaitUnderwriterDecision,
}

impl LoanApplicationFlow {
    /// Creates the Flow for a Worker, whose credit check Step stops the child
    /// Flow through `client`.
    pub fn new(client: Arc<Client>) -> Self {
        Self {
            run_credit_check: RunCreditCheck {
                client: Some(client),
            },
            ..Self::default()
        }
    }

    /// Cancels the application and the credit check it started.
    fn cancel_application(&self, context: &mut Context, reason: String) -> HandlerResult<()> {
        APPLICATION_CANCELED.publish(context, reason)
    }

    /// Records the underwriter's decision.
    fn decide_application(&self, context: &mut Context, decision: String) -> HandlerResult<()> {
        UNDERWRITER_DECIDED.publish(context, decision)
    }
}

impl Flow for LoanApplicationFlow {
    type StartInput = LoanApplication;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.run_credit_check).and(&self.await_underwriter_decision)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&STOPPED_CREDIT_CHECK_FLOW_ID)
            .channel(&APPLICATION_CANCELED)
            .channel(&UNDERWRITER_DECIDED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(CANCEL_APPLICATION, Self::cancel_application)
            .procedure(DECIDE_APPLICATION, Self::decide_application)
    }
}

/// Waits for its child Flow or a cancellation, whichever comes first.
#[derive(Default)]
pub struct RunCreditCheck {
    client: Option<Arc<Client>>,
}

impl RunCreditCheck {
    /// Cancels the child; a missing or already closed child counts as stopped.
    fn stop_credit_check(&self, child_flow_id: &str) -> HandlerResult<()> {
        let client = self
            .client
            .as_ref()
            .ok_or_else(|| HandlerError::new("ClientUnavailable", "the Dex Client is not ready"))?;
        match client.stop_flow(
            child_flow_id,
            StopFlowOptions::cancel().reason("loan application canceled"),
        ) {
            Ok(())
            | Err(
                SdkError::FlowNotFound { .. }
                | SdkError::FlowNotActiveOrNotFound { .. }
                | SdkError::FlowClosing { .. },
            ) => Ok(()),
            Err(error) => Err(HandlerError::from_error(error)),
        }
    }
}

impl Step for RunCreditCheck {
    type Input = LoanApplication;

    fn wait_for(&self, _context: &mut Context, application: Self::Input) -> HandlerResult<Wait> {
        let request = CreditCheckRequest {
            applicant_id: application.applicant_id,
            report_delay_seconds: application.credit_report_delay_seconds,
        };
        let credit_check = SubFlow::run_with_options(
            &CreditCheckFlow::default(),
            request,
            SubFlowOptions::new()
                .timeout(CREDIT_CHECK_TIMEOUT)
                .reuse_policy(SubFlowReusePolicy::RestartIfPreviousExitsAbnormally),
        )
        .map_err(HandlerError::from_error)?;
        // The cancel condition comes first: it wins when both are ready.
        Ok(Wait::any_of([APPLICATION_CANCELED.for_one(), credit_check]))
    }

    fn execute(
        &self,
        context: &mut Context,
        _application: Self::Input,
    ) -> HandlerResult<StepDecision> {
        if let Some(reason) = APPLICATION_CANCELED
            .condition_results(context)?
            .into_iter()
            .next()
        {
            let child_flow_id = SubFlow::flow_id(context)?;
            self.stop_credit_check(&child_flow_id)?;
            STOPPED_CREDIT_CHECK_FLOW_ID.set(context, child_flow_id)?;
            return Ok(StepDecision::force_fail(format!(
                "{APPLICATION_CANCELED_PREFIX}{reason}"
            )));
        }
        let result = SubFlow::condition_result(context)?;
        if result.status() != FlowStatus::Completed {
            return Ok(StepDecision::force_fail(format!(
                "credit check ended with status {:?}",
                result.status()
            )));
        }
        let report: String = result.single_output().map_err(HandlerError::from_error)?;
        Ok(StepDecision::go_to(&AwaitUnderwriterDecision, report))
    }
}

/// Also waits for the cancel Channel, so a cancellation after the credit
/// check still closes the application.
#[derive(Default)]
struct AwaitUnderwriterDecision;

impl Step for AwaitUnderwriterDecision {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _report: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            APPLICATION_CANCELED.for_one(),
            UNDERWRITER_DECIDED.for_one(),
        ]))
    }

    fn execute(&self, context: &mut Context, report: Self::Input) -> HandlerResult<StepDecision> {
        if let Some(reason) = APPLICATION_CANCELED
            .condition_results(context)?
            .into_iter()
            .next()
        {
            return Ok(StepDecision::force_fail(format!(
                "{APPLICATION_CANCELED_PREFIX}{reason}"
            )));
        }
        let decision = UNDERWRITER_DECIDED
            .condition_results(context)?
            .into_iter()
            .next()
            .ok_or_else(|| {
                HandlerError::new("MissingUnderwriterDecision", "no decision consumed")
            })?;
        Ok(StepDecision::graceful_complete(format!(
            "{decision} after {report}"
        )))
    }
}
