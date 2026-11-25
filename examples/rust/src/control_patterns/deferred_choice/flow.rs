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

use std::sync::LazyLock;
use std::time::Duration;

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult,
    Step, StepDecision, StepList, StepOptions, Timer, Wait,
};

/// How long the customer has to respond.
pub const OFFER_EXPIRY: Duration = Duration::from_secs(3 * 24 * 60 * 60);

/// The response window is open until `AwaitOfferResponse` decides. Unset reads as open.
pub const RESPONSE_WINDOW_OPEN: &str = "open";
pub const RESPONSE_WINDOW_CLOSED: &str = "closed";

/// The outcomes of a customer response RPC.
pub const OFFER_RESPONSE_RECORDED: &str = "recorded";
pub const OFFER_RESPONSE_WINDOW_CLOSED: &str = "window closed";

pub static OFFER_ACCEPTED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("offerAccepted"));
pub static OFFER_REJECTED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("offerRejected"));
/// Locked by both response RPCs and by `AwaitOfferResponse::execute`.
pub static OFFER_RESPONSE_WINDOW: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("offerResponseWindow"));

pub const ACCEPT_CUSTOMER_OFFER: Rpc<String, String> = Rpc::new("accept_customer_offer");
pub const REJECT_CUSTOMER_OFFER: Rpc<String, String> = Rpc::new("reject_customer_offer");

/// Routes on the first of three events: the customer accepts, the customer
/// rejects, or the offer expires.
#[derive(Default)]
pub struct OfferResponseFlow {
    await_offer_response: AwaitOfferResponse,
    fulfill_offer: FulfillOffer,
    close_rejected_offer: CloseRejectedOffer,
    route_response_before_expiry: RouteResponseBeforeExpiry,
    expire_offer: ExpireOffer,
}

impl OfferResponseFlow {
    /// Records that the customer accepted the offer.
    /// Returns `OFFER_RESPONSE_WINDOW_CLOSED` once the offer has decided.
    pub fn accept_customer_offer(
        &self,
        context: &mut Context,
        note: String,
    ) -> HandlerResult<RpcResult<String>> {
        publish_while_window_open(context, &OFFER_ACCEPTED, note)
    }

    /// Records that the customer rejected the offer.
    /// Returns `OFFER_RESPONSE_WINDOW_CLOSED` once the offer has decided.
    pub fn reject_customer_offer(
        &self,
        context: &mut Context,
        note: String,
    ) -> HandlerResult<RpcResult<String>> {
        publish_while_window_open(context, &OFFER_REJECTED, note)
    }
}

/// Publishes the response unless the window is closed.
/// The publish commits together with this check, because the RPC holds the window lock.
fn publish_while_window_open(
    context: &mut Context,
    response: &Channel<String>,
    note: String,
) -> HandlerResult<RpcResult<String>> {
    let window = OFFER_RESPONSE_WINDOW.get(context)?;
    if window.as_deref() == Some(RESPONSE_WINDOW_CLOSED) {
        return Ok(RpcResult::new(OFFER_RESPONSE_WINDOW_CLOSED.to_string()));
    }
    response.publish(context, note)?;
    Ok(RpcResult::new(OFFER_RESPONSE_RECORDED.to_string()))
}

impl Flow for OfferResponseFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.await_offer_response)
            .and(&self.fulfill_offer)
            .and(&self.close_rejected_offer)
            .and(&self.route_response_before_expiry)
            .and(&self.expire_offer)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&OFFER_RESPONSE_WINDOW)
            .channel(&OFFER_ACCEPTED)
            .channel(&OFFER_REJECTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(
                ACCEPT_CUSTOMER_OFFER.lock(OFFER_RESPONSE_WINDOW.lock()),
                Self::accept_customer_offer,
            )
            .function(
                REJECT_CUSTOMER_OFFER.lock(OFFER_RESPONSE_WINDOW.lock()),
                Self::reject_customer_offer,
            )
    }
}

// dex:group group-id:decide group-label:"Decide"
// dex:explanation text:"Waits for an acceptance, a rejection, or the 3-day deadline, closes the response window, and routes on the winner."
#[derive(Default)]
pub struct AwaitOfferResponse;

impl Step for AwaitOfferResponse {
    type Input = String;

    /// Locks the response window in Execute, the same lock as the response RPCs.
    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(OFFER_RESPONSE_WINDOW.lock())
    }

    fn wait_for(&self, _context: &mut Context, _offer_id: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            OFFER_ACCEPTED.for_one(),
            OFFER_REJECTED.for_one(),
            Timer::by_duration(OFFER_EXPIRY),
        ]))
    }

    fn execute(&self, context: &mut Context, offer_id: Self::Input) -> HandlerResult<StepDecision> {
        // Every later response RPC now returns OFFER_RESPONSE_WINDOW_CLOSED.
        OFFER_RESPONSE_WINDOW.set(context, RESPONSE_WINDOW_CLOSED.to_string())?;
        if context.has_any_timer_fired() {
            // A response committed after the Timer won but before this Execute took the lock.
            if OFFER_ACCEPTED.size(context)? + OFFER_REJECTED.size(context)? > 0 {
                return Ok(StepDecision::go_to(&RouteResponseBeforeExpiry, offer_id));
            }
            return Ok(StepDecision::go_to(&ExpireOffer, offer_id));
        }
        if !OFFER_ACCEPTED.condition_results(context)?.is_empty() {
            return Ok(StepDecision::go_to(&FulfillOffer, offer_id));
        }
        Ok(StepDecision::go_to(&CloseRejectedOffer, offer_id))
    }
}

// dex:group group-id:decide group-label:"Decide"
// dex:explanation text:"Takes the response that the customer sent before the window closed and routes on it."
#[derive(Default)]
pub struct RouteResponseBeforeExpiry;

impl Step for RouteResponseBeforeExpiry {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _offer_id: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            OFFER_ACCEPTED.for_one(),
            OFFER_REJECTED.for_one(),
        ]))
    }

    fn execute(&self, context: &mut Context, offer_id: Self::Input) -> HandlerResult<StepDecision> {
        if !OFFER_ACCEPTED.condition_results(context)?.is_empty() {
            return Ok(StepDecision::go_to(&FulfillOffer, offer_id));
        }
        Ok(StepDecision::go_to(&CloseRejectedOffer, offer_id))
    }
}

// dex:group group-id:outcome group-label:"Outcome"
// dex:explanation text:"Completes the Flow with the fulfilled offer."
#[derive(Default)]
pub struct FulfillOffer;

impl Step for FulfillOffer {
    type Input = String;

    fn execute(
        &self,
        _context: &mut Context,
        offer_id: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!(
            "fulfilled {offer_id}"
        )))
    }
}

// dex:group group-id:outcome group-label:"Outcome"
// dex:explanation text:"Completes the Flow with the rejected offer."
#[derive(Default)]
pub struct CloseRejectedOffer;

impl Step for CloseRejectedOffer {
    type Input = String;

    fn execute(
        &self,
        _context: &mut Context,
        offer_id: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!(
            "rejected {offer_id}"
        )))
    }
}

// dex:group group-id:outcome group-label:"Outcome"
// dex:explanation text:"Completes the Flow with the expired offer."
#[derive(Default)]
pub struct ExpireOffer;

impl Step for ExpireOffer {
    type Input = String;

    fn execute(
        &self,
        _context: &mut Context,
        offer_id: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!(
            "expired {offer_id}"
        )))
    }
}
