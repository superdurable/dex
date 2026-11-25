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

//! Runs the Decision with Deadline Steps beside a gated Step, so the Flow stays
//! open after the decision and late responses reach the window check.

use std::sync::{Condvar, LazyLock, Mutex};
use std::time::{Duration, Instant};

use dex_examples_rust::control_patterns::deferred_choice::{
    AwaitOfferResponse, CloseRejectedOffer, ExpireOffer, FulfillOffer, OFFER_ACCEPTED,
    OFFER_REJECTED, OFFER_RESPONSE_WINDOW, OfferResponseFlow, RouteResponseBeforeExpiry,
};
use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Registry, Rpc, RpcList, RpcResult,
    SdkResult, Step, StepDecision, StepList, StepMovement, Wait,
};

static OFFER_PROBE_GATE_OPENED: LazyLock<Channel<()>> =
    LazyLock::new(|| Channel::new("offerProbeGateOpened"));

pub(crate) const ACCEPT_WINDOW_PROBE_OFFER: Rpc<String, String> =
    Rpc::new("accept_window_probe_offer");
pub(crate) const REJECT_WINDOW_PROBE_OFFER: Rpc<String, String> =
    Rpc::new("reject_window_probe_offer");
pub(crate) const ACCEPT_WINDOW_PROBE_OFFER_WHEN_RELEASED: Rpc<String, String> =
    Rpc::new("accept_window_probe_offer_when_released");
pub(crate) const OPEN_WINDOW_PROBE_GATE: Rpc<(), ()> = Rpc::new("open_window_probe_gate");

/// Lets a test hold one response RPC inside its window lock.
pub(crate) static HELD_RESPONSE_GATE: LazyLock<HeldResponseGate> =
    LazyLock::new(HeldResponseGate::default);

#[derive(Default)]
struct HeldResponseState {
    entered: bool,
    released: bool,
}

#[derive(Default)]
pub(crate) struct HeldResponseGate {
    state: Mutex<HeldResponseState>,
    changed: Condvar,
}

impl HeldResponseGate {
    fn enter_and_wait_for_release(&self) {
        let mut state = self.state.lock().expect("held response state lock");
        state.entered = true;
        self.changed.notify_all();
        while !state.released {
            state = self.changed.wait(state).expect("held response state lock");
        }
    }

    /// Returns whether the held RPC entered its handler before the timeout.
    pub(crate) fn wait_until_entered(&self, timeout: Duration) -> bool {
        let state = self.state.lock().expect("held response state lock");
        let (state, _) = self
            .changed
            .wait_timeout_while(state, timeout, |state| !state.entered)
            .expect("held response state lock");
        state.entered
    }

    pub(crate) fn release(&self) {
        let mut state = self.state.lock().expect("held response state lock");
        state.released = true;
        self.changed.notify_all();
    }
}

/// Releases the held RPC when dropped, so a failed assertion never leaves the Worker blocked.
pub(crate) struct HeldResponseReleaseGuard;

impl Drop for HeldResponseReleaseGuard {
    fn drop(&mut self) {
        HELD_RESPONSE_GATE.release();
    }
}

const STEP_METHOD_PENDING_REASON_WAITING_FOR_LOCKS: u64 = 3;

/// Polls the Dex Web API until the Execute of `step_type` waits for its locks.
/// Returns whether it did before the timeout.
pub(crate) fn wait_until_execute_waits_for_locks(
    flow_id: &str,
    run_id: &str,
    step_type: &str,
    timeout: Duration,
) -> bool {
    let web_address =
        std::env::var("DEX_WEB_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8802".to_string());
    let url = format!("http://{web_address}/api/flows/state");
    let http = reqwest::blocking::Client::new();
    let deadline = Instant::now() + timeout;
    while Instant::now() < deadline {
        let state = http
            .get(&url)
            .query(&[("flowId", flow_id), ("runId", run_id)])
            .send()
            .and_then(|response| response.error_for_status())
            .and_then(|response| response.json::<serde_json::Value>());
        if let Ok(state) = state
            && execute_waits_for_locks(&state, step_type)
        {
            return true;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    false
}

fn execute_waits_for_locks(state: &serde_json::Value, step_type: &str) -> bool {
    state["activeStepExecutions"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(|step| step["stepType"] == step_type)
        .any(|step| {
            step["executeInvocation"]["pendingReason"].as_u64()
                == Some(STEP_METHOD_PENDING_REASON_WAITING_FOR_LOCKS)
        })
}

pub(crate) fn register_offer_response_window_probe(registry: Registry) -> SdkResult<Registry> {
    registry
        .register(OfferResponseWindowProbeFlow::default())?
        .register(OfferResponseDecisionGateProbeFlow::default())
}

static OFFER_DECISION_GATE_OPENED: LazyLock<Channel<()>> =
    LazyLock::new(|| Channel::new("offerDecisionGateOpened"));

pub(crate) const ACCEPT_GATED_PROBE_OFFER: Rpc<String, String> =
    Rpc::new("accept_gated_probe_offer");
pub(crate) const REJECT_GATED_PROBE_OFFER: Rpc<String, String> =
    Rpc::new("reject_gated_probe_offer");
pub(crate) const OPEN_OFFER_DECISION_GATE: Rpc<(), ()> = Rpc::new("open_offer_decision_gate");

/// Holds AwaitOfferResponse behind a gate, so replies commit before its wait exists.
#[derive(Default)]
pub(crate) struct OfferResponseDecisionGateProbeFlow {
    await_offer_decision_gate: AwaitOfferDecisionGate,
    await_offer_response: AwaitOfferResponse,
    fulfill_offer: FulfillOffer,
    close_rejected_offer: CloseRejectedOffer,
    route_response_before_expiry: RouteResponseBeforeExpiry,
    expire_offer: ExpireOffer,
}

impl OfferResponseDecisionGateProbeFlow {
    fn accept_gated_probe_offer(
        &self,
        context: &mut Context,
        note: String,
    ) -> HandlerResult<RpcResult<String>> {
        OfferResponseFlow::default().accept_customer_offer(context, note)
    }

    fn reject_gated_probe_offer(
        &self,
        context: &mut Context,
        note: String,
    ) -> HandlerResult<RpcResult<String>> {
        OfferResponseFlow::default().reject_customer_offer(context, note)
    }

    fn open_offer_decision_gate(&self, context: &mut Context) -> HandlerResult<()> {
        OFFER_DECISION_GATE_OPENED.publish(context, ())
    }
}

impl Flow for OfferResponseDecisionGateProbeFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.await_offer_decision_gate)
            .and(&self.await_offer_response)
            .and(&self.fulfill_offer)
            .and(&self.close_rejected_offer)
            .and(&self.route_response_before_expiry)
            .and(&self.expire_offer)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&OFFER_RESPONSE_WINDOW)
            .channel(&OFFER_DECISION_GATE_OPENED)
            .channel(&OFFER_ACCEPTED)
            .channel(&OFFER_REJECTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(
                ACCEPT_GATED_PROBE_OFFER.lock(OFFER_RESPONSE_WINDOW.lock()),
                Self::accept_gated_probe_offer,
            )
            .function(
                REJECT_GATED_PROBE_OFFER.lock(OFFER_RESPONSE_WINDOW.lock()),
                Self::reject_gated_probe_offer,
            )
            .procedure_without_input(OPEN_OFFER_DECISION_GATE, Self::open_offer_decision_gate)
    }
}

#[derive(Default)]
struct AwaitOfferDecisionGate;

impl Step for AwaitOfferDecisionGate {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _offer_id: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(OFFER_DECISION_GATE_OPENED.for_one()))
    }

    fn execute(
        &self,
        _context: &mut Context,
        offer_id: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&AwaitOfferResponse, offer_id))
    }
}

#[derive(Default)]
pub(crate) struct OfferResponseWindowProbeFlow {
    open_offer_probe: OpenOfferProbe,
    hold_offer_probe_open: HoldOfferProbeOpen,
    await_offer_response: AwaitOfferResponse,
    fulfill_offer: FulfillOffer,
    close_rejected_offer: CloseRejectedOffer,
    route_response_before_expiry: RouteResponseBeforeExpiry,
    expire_offer: ExpireOffer,
}

impl OfferResponseWindowProbeFlow {
    fn accept_probe_offer(
        &self,
        context: &mut Context,
        note: String,
    ) -> HandlerResult<RpcResult<String>> {
        OfferResponseFlow::default().accept_customer_offer(context, note)
    }

    fn reject_probe_offer(
        &self,
        context: &mut Context,
        note: String,
    ) -> HandlerResult<RpcResult<String>> {
        OfferResponseFlow::default().reject_customer_offer(context, note)
    }

    /// Holds the window lock until the test releases it.
    fn accept_probe_offer_when_released(
        &self,
        context: &mut Context,
        note: String,
    ) -> HandlerResult<RpcResult<String>> {
        HELD_RESPONSE_GATE.enter_and_wait_for_release();
        OfferResponseFlow::default().accept_customer_offer(context, note)
    }

    fn open_probe_gate(&self, context: &mut Context) -> HandlerResult<()> {
        OFFER_PROBE_GATE_OPENED.publish(context, ())
    }
}

impl Flow for OfferResponseWindowProbeFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.open_offer_probe)
            .and(&self.hold_offer_probe_open)
            .and(&self.await_offer_response)
            .and(&self.fulfill_offer)
            .and(&self.close_rejected_offer)
            .and(&self.route_response_before_expiry)
            .and(&self.expire_offer)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&OFFER_RESPONSE_WINDOW)
            .channel(&OFFER_PROBE_GATE_OPENED)
            .channel(&OFFER_ACCEPTED)
            .channel(&OFFER_REJECTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(
                ACCEPT_WINDOW_PROBE_OFFER.lock(OFFER_RESPONSE_WINDOW.lock()),
                Self::accept_probe_offer,
            )
            .function(
                REJECT_WINDOW_PROBE_OFFER.lock(OFFER_RESPONSE_WINDOW.lock()),
                Self::reject_probe_offer,
            )
            .function(
                ACCEPT_WINDOW_PROBE_OFFER_WHEN_RELEASED.lock(OFFER_RESPONSE_WINDOW.lock()),
                Self::accept_probe_offer_when_released,
            )
            .procedure_without_input(OPEN_WINDOW_PROBE_GATE, Self::open_probe_gate)
    }
}

#[derive(Default)]
struct OpenOfferProbe;

impl Step for OpenOfferProbe {
    type Input = String;

    fn execute(
        &self,
        _context: &mut Context,
        offer_id: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&AwaitOfferResponse, offer_id),
            StepMovement::to(&HoldOfferProbeOpen, ()),
        ]))
    }
}

/// Keeps the Flow open until the test opens the gate.
#[derive(Default)]
struct HoldOfferProbeOpen;

impl Step for HoldOfferProbeOpen {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(OFFER_PROBE_GATE_OPENED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}
