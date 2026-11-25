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
use std::time::Duration;

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

pub(crate) fn register_offer_response_window_probe(registry: Registry) -> SdkResult<Registry> {
    registry.register(OfferResponseWindowProbeFlow::default())
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
