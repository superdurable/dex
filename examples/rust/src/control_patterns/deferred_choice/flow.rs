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
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step, StepDecision,
    StepList, Timer, Wait,
};

/// How long the customer has to respond.
pub const OFFER_EXPIRY: Duration = Duration::from_secs(3 * 24 * 60 * 60);

pub static OFFER_ACCEPTED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("offerAccepted"));
pub static OFFER_REJECTED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("offerRejected"));

pub const ACCEPT_CUSTOMER_OFFER: Rpc<String, ()> = Rpc::new("accept_customer_offer");
pub const REJECT_CUSTOMER_OFFER: Rpc<String, ()> = Rpc::new("reject_customer_offer");

/// Routes on the first of three events: the customer accepts, the customer
/// rejects, or the offer expires.
#[derive(Default)]
pub struct OfferResponseFlow {
    await_offer_response: AwaitOfferResponse,
    fulfill_offer: FulfillOffer,
    close_rejected_offer: CloseRejectedOffer,
    expire_offer: ExpireOffer,
}

impl OfferResponseFlow {
    /// Records that the customer accepted the offer.
    fn accept_customer_offer(&self, context: &mut Context, note: String) -> HandlerResult<()> {
        OFFER_ACCEPTED.publish(context, note)
    }

    /// Records that the customer rejected the offer.
    fn reject_customer_offer(&self, context: &mut Context, note: String) -> HandlerResult<()> {
        OFFER_REJECTED.publish(context, note)
    }
}

impl Flow for OfferResponseFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.await_offer_response)
            .and(&self.fulfill_offer)
            .and(&self.close_rejected_offer)
            .and(&self.expire_offer)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&OFFER_ACCEPTED)
            .channel(&OFFER_REJECTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(ACCEPT_CUSTOMER_OFFER, Self::accept_customer_offer)
            .procedure(REJECT_CUSTOMER_OFFER, Self::reject_customer_offer)
    }
}

/// Waits for whichever event happens first and routes on it.
#[derive(Default)]
pub struct AwaitOfferResponse;

impl Step for AwaitOfferResponse {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _offer_id: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            OFFER_ACCEPTED.for_one(),
            OFFER_REJECTED.for_one(),
            Timer::by_duration(OFFER_EXPIRY),
        ]))
    }

    fn execute(&self, context: &mut Context, offer_id: Self::Input) -> HandlerResult<StepDecision> {
        if context.has_any_timer_fired() {
            return Ok(StepDecision::go_to(&ExpireOffer, offer_id));
        }
        if !OFFER_ACCEPTED.condition_results(context)?.is_empty() {
            return Ok(StepDecision::go_to(&FulfillOffer, offer_id));
        }
        Ok(StepDecision::go_to(&CloseRejectedOffer, offer_id))
    }
}

#[derive(Default)]
struct FulfillOffer;

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

#[derive(Default)]
struct CloseRejectedOffer;

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

#[derive(Default)]
struct ExpireOffer;

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
