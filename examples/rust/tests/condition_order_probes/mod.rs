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

//! Test-only probe Flows. A gate Step holds the first wait, so both competing
//! messages commit before an AnyOf with the example's condition order registers.

use std::sync::LazyLock;

use dex_examples_rust::control_patterns::cancel_case::{APPLICATION_CANCELED, UNDERWRITER_DECIDED};
use dex_examples_rust::control_patterns::deferred_choice::{
    OFFER_ACCEPTED, OFFER_EXPIRY, OFFER_REJECTED,
};
use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Registry, Rpc, RpcList, SdkResult,
    Step, StepDecision, StepList, Timer, Wait,
};
use serde::{Deserialize, Serialize};

static PROBE_GATE_OPENED: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("probeGateOpened"));

pub(crate) const ACCEPT_PROBE_OFFER: Rpc<String, ()> = Rpc::new("accept_probe_offer");
pub(crate) const REJECT_PROBE_OFFER: Rpc<String, ()> = Rpc::new("reject_probe_offer");
pub(crate) const OPEN_OFFER_PROBE_GATE: Rpc<(), ()> = Rpc::new("open_offer_probe_gate");
pub(crate) const CANCEL_PROBE_APPLICATION: Rpc<String, ()> = Rpc::new("cancel_probe_application");
pub(crate) const DECIDE_PROBE_APPLICATION: Rpc<String, ()> = Rpc::new("decide_probe_application");
pub(crate) const OPEN_DECISION_PROBE_GATE: Rpc<(), ()> = Rpc::new("open_decision_probe_gate");

/// Which condition won and how many messages the losing Channel still holds.
#[derive(Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct ConditionOrderOutcome {
    pub(crate) winner: String,
    pub(crate) loser_pending_count: usize,
}

impl ConditionOrderOutcome {
    pub(crate) fn new(winner: &str, loser_pending_count: usize) -> Self {
        Self {
            winner: winner.to_string(),
            loser_pending_count,
        }
    }
}

/// Adds the probe Flows to a test registry.
pub(crate) fn register_condition_order_probes(registry: Registry) -> SdkResult<Registry> {
    registry
        .register(DeferredChoiceOrderProbeFlow::default())?
        .register(CancelCaseOrderProbeFlow::default())
}

#[derive(Default)]
pub(crate) struct DeferredChoiceOrderProbeFlow {
    hold_offer_probe: HoldOfferProbe,
    await_offer_probe_response: AwaitOfferProbeResponse,
}

impl DeferredChoiceOrderProbeFlow {
    fn accept_probe_offer(&self, context: &mut Context, note: String) -> HandlerResult<()> {
        OFFER_ACCEPTED.publish(context, note)
    }

    fn reject_probe_offer(&self, context: &mut Context, note: String) -> HandlerResult<()> {
        OFFER_REJECTED.publish(context, note)
    }

    fn open_probe_gate(&self, context: &mut Context) -> HandlerResult<()> {
        PROBE_GATE_OPENED.publish(context, ())
    }
}

impl Flow for DeferredChoiceOrderProbeFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.hold_offer_probe).and(&self.await_offer_probe_response)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&PROBE_GATE_OPENED)
            .channel(&OFFER_ACCEPTED)
            .channel(&OFFER_REJECTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(ACCEPT_PROBE_OFFER, Self::accept_probe_offer)
            .procedure(REJECT_PROBE_OFFER, Self::reject_probe_offer)
            .procedure_without_input(OPEN_OFFER_PROBE_GATE, Self::open_probe_gate)
    }
}

#[derive(Default)]
struct HoldOfferProbe;

impl Step for HoldOfferProbe {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(PROBE_GATE_OPENED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&AwaitOfferProbeResponse, ()))
    }
}

#[derive(Default)]
struct AwaitOfferProbeResponse;

impl Step for AwaitOfferProbeResponse {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            OFFER_ACCEPTED.for_one(),
            OFFER_REJECTED.for_one(),
            Timer::by_duration(OFFER_EXPIRY),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        if !OFFER_ACCEPTED.condition_results(context)?.is_empty() {
            return Ok(StepDecision::graceful_complete(ConditionOrderOutcome::new(
                "accepted",
                OFFER_REJECTED.size(context)?,
            )));
        }
        Ok(StepDecision::graceful_complete(ConditionOrderOutcome::new(
            "rejected",
            OFFER_ACCEPTED.size(context)?,
        )))
    }
}

#[derive(Default)]
pub(crate) struct CancelCaseOrderProbeFlow {
    hold_decision_probe: HoldDecisionProbe,
    await_probe_decision: AwaitProbeDecision,
}

impl CancelCaseOrderProbeFlow {
    fn cancel_probe_application(&self, context: &mut Context, reason: String) -> HandlerResult<()> {
        APPLICATION_CANCELED.publish(context, reason)
    }

    fn decide_probe_application(
        &self,
        context: &mut Context,
        decision: String,
    ) -> HandlerResult<()> {
        UNDERWRITER_DECIDED.publish(context, decision)
    }

    fn open_probe_gate(&self, context: &mut Context) -> HandlerResult<()> {
        PROBE_GATE_OPENED.publish(context, ())
    }
}

impl Flow for CancelCaseOrderProbeFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.hold_decision_probe).and(&self.await_probe_decision)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&PROBE_GATE_OPENED)
            .channel(&APPLICATION_CANCELED)
            .channel(&UNDERWRITER_DECIDED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(CANCEL_PROBE_APPLICATION, Self::cancel_probe_application)
            .procedure(DECIDE_PROBE_APPLICATION, Self::decide_probe_application)
            .procedure_without_input(OPEN_DECISION_PROBE_GATE, Self::open_probe_gate)
    }
}

#[derive(Default)]
struct HoldDecisionProbe;

impl Step for HoldDecisionProbe {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(PROBE_GATE_OPENED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&AwaitProbeDecision, ()))
    }
}

#[derive(Default)]
struct AwaitProbeDecision;

impl Step for AwaitProbeDecision {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            APPLICATION_CANCELED.for_one(),
            UNDERWRITER_DECIDED.for_one(),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        if !APPLICATION_CANCELED.condition_results(context)?.is_empty() {
            return Ok(StepDecision::graceful_complete(ConditionOrderOutcome::new(
                "canceled",
                UNDERWRITER_DECIDED.size(context)?,
            )));
        }
        Ok(StepDecision::graceful_complete(ConditionOrderOutcome::new(
            "decided",
            APPLICATION_CANCELED.size(context)?,
        )))
    }
}
