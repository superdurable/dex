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

//! Partial joins: Structured Discriminator (WCP-9), Blocking Discriminator
//! (WCP-28), Cancelling Discriminator (WCP-29), Structured Partial Join
//! (WCP-30), Blocking Partial Join (WCP-31), and Cancelling Partial Join
//! (WCP-32).

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerError, HandlerResult, Join, JoinRound,
    PersistenceSchema, Rpc, RpcList, RpcResult, Step, StepDecision, StepList, StepMovement, Wait,
};
use serde::{Deserialize, Serialize};

use super::{round_branch, sorted};

const SUPPLIER_COUNT: usize = 3;
const CARRIER_COUNT: usize = 3;
const BLOCKING_BRANCH_COUNT: usize = 3;
const BLOCKING_JOIN_ROUND_COUNT: u32 = 2;

static SUPPLIER_A_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("supplierAReleased"));
static SUPPLIER_B_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("supplierBReleased"));
static SUPPLIER_C_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("supplierCReleased"));
static QUOTE_RECEIVED: LazyLock<Join<String>> = LazyLock::new(|| Join::new("quoteReceived"));
pub static IGNORED_QUOTES: LazyLock<Attribute<IgnoredQuotes>> =
    LazyLock::new(|| Attribute::new("ignoredQuotes"));

pub const RELEASE_SUPPLIER_QUOTE: Rpc<String, ()> = Rpc::new("release_supplier_quote");

/// One round of the structured partial join.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct QuoteRound {
    pub required_quotes: usize,
    pub round: u32,
    pub round_count: u32,
    /// The Join round to reset before this round starts; `None` in round 1.
    pub previous_round: Option<JoinRound>,
}

/// The input of the join Step of one quote round.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct QuoteRoundJoin {
    pub quote_round: QuoteRound,
    pub join_round: JoinRound,
}

/// The quotes the join accepted in one round.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AcceptedQuotes {
    pub round: u32,
    pub accepted_quotes: Vec<String>,
}

/// The late quotes of a round, read by the next round's split Step.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct IgnoredQuotes {
    pub round: u32,
    pub ignored_quotes: Vec<String>,
}

/// WCP-9 Structured Discriminator (`required_quotes` = 1) and WCP-30
/// Structured Partial Join (`required_quotes` = 2). Quotes are keyed by
/// Join round, and the next round's split resets the rest before the join re-enables.
#[derive(Default)]
pub struct StructuredPartialJoinFlow {
    request_quotes: RequestQuotes,
    quote_from_supplier_a: QuoteFromSupplierA,
    quote_from_supplier_b: QuoteFromSupplierB,
    quote_from_supplier_c: QuoteFromSupplierC,
    accept_quotes: AcceptQuotes,
    place_order: PlaceOrder,
}

impl StructuredPartialJoinFlow {
    fn release_supplier_quote(&self, context: &mut Context, supplier: String) -> HandlerResult<()> {
        let channel = match supplier.as_str() {
            "A" => &SUPPLIER_A_RELEASED,
            "B" => &SUPPLIER_B_RELEASED,
            "C" => &SUPPLIER_C_RELEASED,
            _ => {
                return Err(unknown_letter_error(
                    "InvalidSupplier",
                    "supplier",
                    &supplier,
                ));
            }
        };
        channel.publish(context, supplier)
    }
}

impl Flow for StructuredPartialJoinFlow {
    type StartInput = QuoteRound;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.request_quotes)
            .and(&self.quote_from_supplier_a)
            .and(&self.quote_from_supplier_b)
            .and(&self.quote_from_supplier_c)
            .and(&self.accept_quotes)
            .and(&self.place_order)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&SUPPLIER_A_RELEASED)
            .channel(&SUPPLIER_B_RELEASED)
            .channel(&SUPPLIER_C_RELEASED)
            .join(&QUOTE_RECEIVED)
            .attribute(&IGNORED_QUOTES)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(RELEASE_SUPPLIER_QUOTE, Self::release_supplier_quote)
    }
}

#[derive(Default)]
pub struct RequestQuotes;

impl Step for RequestQuotes {
    type Input = QuoteRound;

    /// Resets the previous round: waits for the quotes its join did not accept.
    fn wait_for(&self, _context: &mut Context, round: Self::Input) -> HandlerResult<Wait> {
        match &round.previous_round {
            None => Ok(Wait::skip_immediately()),
            Some(previous_round) => Ok(Wait::until(
                QUOTE_RECEIVED.for_remaining_branches(previous_round, round.required_quotes),
            )),
        }
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        if !(1..=SUPPLIER_COUNT).contains(&round.required_quotes) {
            return Ok(StepDecision::force_fail(format!(
                "required quotes must be between 1 and {SUPPLIER_COUNT}, got {}",
                round.required_quotes
            )));
        }
        if round.round < 1 || round.previous_round.is_some() != (round.round > 1) {
            return Ok(StepDecision::force_fail(format!(
                "round must be at least 1 and carry a previous round exactly after round 1, got round {}",
                round.round
            )));
        }
        if let Some(previous_round) = &round.previous_round {
            let late_quotes = QUOTE_RECEIVED.remaining_branch_results(context, previous_round)?;
            IGNORED_QUOTES.set(
                context,
                IgnoredQuotes {
                    round: round.round - 1,
                    ignored_quotes: sorted(late_quotes),
                },
            )?;
        }
        let movements = QUOTE_RECEIVED.fork(
            context,
            vec![
                round_branch(QuoteFromSupplierA),
                round_branch(QuoteFromSupplierB),
                round_branch(QuoteFromSupplierC),
            ],
            |join_round| {
                StepMovement::to(
                    &AcceptQuotes,
                    QuoteRoundJoin {
                        quote_round: round,
                        join_round,
                    },
                )
            },
        )?;
        Ok(StepDecision::go_to_many(movements))
    }
}

#[derive(Default)]
pub struct QuoteFromSupplierA;

impl Step for QuoteFromSupplierA {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, _round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(SUPPLIER_A_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        QUOTE_RECEIVED.end_branch(context, &round, "A".to_string())
    }
}

#[derive(Default)]
struct QuoteFromSupplierB;

impl Step for QuoteFromSupplierB {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, _round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(SUPPLIER_B_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        QUOTE_RECEIVED.end_branch(context, &round, "B".to_string())
    }
}

#[derive(Default)]
pub struct QuoteFromSupplierC;

impl Step for QuoteFromSupplierC {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, _round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(SUPPLIER_C_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        QUOTE_RECEIVED.end_branch(context, &round, "C".to_string())
    }
}

#[derive(Default)]
struct AcceptQuotes;

impl Step for AcceptQuotes {
    type Input = QuoteRoundJoin;

    fn wait_for(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(QUOTE_RECEIVED.for_branches(
            &input.join_round,
            input.quote_round.required_quotes,
        )))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let quotes = QUOTE_RECEIVED.branch_results(context, &input.join_round)?;
        let round = input.quote_round;
        let mut movements = vec![StepMovement::to(
            &PlaceOrder,
            AcceptedQuotes {
                round: round.round,
                accepted_quotes: sorted(quotes),
            },
        )];
        if round.round < round.round_count {
            movements.push(StepMovement::to(
                &RequestQuotes,
                QuoteRound {
                    round: round.round + 1,
                    previous_round: Some(input.join_round),
                    ..round
                },
            ));
        }
        // The last round's late quotes stay pending until the Flow closes.
        Ok(StepDecision::go_to_many(movements))
    }
}

#[derive(Default)]
pub struct PlaceOrder;

impl Step for PlaceOrder {
    type Input = AcceptedQuotes;

    fn execute(
        &self,
        _context: &mut Context,
        accepted_quotes: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(accepted_quotes))
    }
}

static BRANCH_A_SIGNAL: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("branchASignal"));
static BRANCH_B_SIGNAL: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("branchBSignal"));
static BRANCH_C_SIGNAL: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("branchCSignal"));
static BRANCH_COMPLETED: LazyLock<Join<String>> = LazyLock::new(|| Join::new("branchCompleted"));

pub const SEND_BRANCH_SIGNAL: Rpc<String, ()> = Rpc::new("send_branch_signal");
pub const GET_QUEUED_BRANCH_SIGNAL_COUNT: Rpc<(), usize> =
    Rpc::new("get_queued_branch_signal_count");

/// One round of the blocking join.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BlockingJoinRound {
    pub required_signals: usize,
    pub round: u32,
    /// The Join round to reset before this round starts; `None` in round 1.
    pub previous_round: Option<JoinRound>,
}

/// The input of the join Step of one blocking round.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BlockingRoundJoin {
    pub blocking_round: BlockingJoinRound,
    pub join_round: JoinRound,
}

/// The branches that fired the blocking join in one round.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FiredBranchesRound {
    pub round: u32,
    pub branches: Vec<String>,
}

/// WCP-28 Blocking Discriminator (`required_signals` = 1) and WCP-31
/// Blocking Partial Join (`required_signals` = 2). No Step waits on a
/// next-round enablement until the next round's split resets the current round.
#[derive(Default)]
pub struct BlockingPartialJoinFlow {
    start_blocking_round: StartBlockingRound,
    signal_from_branch_a: SignalFromBranchA,
    signal_from_branch_b: SignalFromBranchB,
    signal_from_branch_c: SignalFromBranchC,
    blocking_join: BlockingJoin,
    process_round: ProcessRound,
}

impl BlockingPartialJoinFlow {
    fn send_branch_signal(&self, context: &mut Context, branch: String) -> HandlerResult<()> {
        let channel = match branch.as_str() {
            "A" => &BRANCH_A_SIGNAL,
            "B" => &BRANCH_B_SIGNAL,
            "C" => &BRANCH_C_SIGNAL,
            _ => return Err(unknown_letter_error("InvalidBranch", "branch", &branch)),
        };
        channel.publish(context, branch)
    }

    fn get_queued_branch_signal_count(
        &self,
        context: &mut Context,
    ) -> HandlerResult<RpcResult<usize>> {
        let queued_signal_count = BRANCH_A_SIGNAL.pending_messages(context)?.len()
            + BRANCH_B_SIGNAL.pending_messages(context)?.len()
            + BRANCH_C_SIGNAL.pending_messages(context)?.len();
        Ok(RpcResult::new(queued_signal_count))
    }
}

impl Flow for BlockingPartialJoinFlow {
    type StartInput = BlockingJoinRound;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_blocking_round)
            .and(&self.signal_from_branch_a)
            .and(&self.signal_from_branch_b)
            .and(&self.signal_from_branch_c)
            .and(&self.blocking_join)
            .and(&self.process_round)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&BRANCH_A_SIGNAL)
            .channel(&BRANCH_B_SIGNAL)
            .channel(&BRANCH_C_SIGNAL)
            .join(&BRANCH_COMPLETED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(SEND_BRANCH_SIGNAL, Self::send_branch_signal)
            .function_without_input(
                GET_QUEUED_BRANCH_SIGNAL_COUNT
                    .load_channel(&BRANCH_A_SIGNAL)
                    .load_channel(&BRANCH_B_SIGNAL)
                    .load_channel(&BRANCH_C_SIGNAL),
                Self::get_queued_branch_signal_count,
            )
    }
}

#[derive(Default)]
pub struct StartBlockingRound;

impl Step for StartBlockingRound {
    type Input = BlockingJoinRound;

    /// Resets the previous round: waits for the branches that did not fire its join.
    fn wait_for(&self, _context: &mut Context, round: Self::Input) -> HandlerResult<Wait> {
        match &round.previous_round {
            None => Ok(Wait::skip_immediately()),
            Some(previous_round) => Ok(Wait::until(
                BRANCH_COMPLETED.for_remaining_branches(previous_round, round.required_signals),
            )),
        }
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        if !(1..BLOCKING_BRANCH_COUNT).contains(&round.required_signals) {
            return Ok(StepDecision::force_fail(format!(
                "required signals must be 1 or 2, got {}",
                round.required_signals
            )));
        }
        let movements = BRANCH_COMPLETED.fork(
            context,
            vec![
                round_branch(SignalFromBranchA),
                round_branch(SignalFromBranchB),
                round_branch(SignalFromBranchC),
            ],
            |join_round| {
                StepMovement::to(
                    &BlockingJoin,
                    BlockingRoundJoin {
                        blocking_round: round,
                        join_round,
                    },
                )
            },
        )?;
        Ok(StepDecision::go_to_many(movements))
    }
}

#[derive(Default)]
struct SignalFromBranchA;

impl Step for SignalFromBranchA {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, _round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(BRANCH_A_SIGNAL.for_one()))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        BRANCH_COMPLETED.end_branch(context, &round, "A".to_string())
    }
}

#[derive(Default)]
struct SignalFromBranchB;

impl Step for SignalFromBranchB {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, _round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(BRANCH_B_SIGNAL.for_one()))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        BRANCH_COMPLETED.end_branch(context, &round, "B".to_string())
    }
}

#[derive(Default)]
struct SignalFromBranchC;

impl Step for SignalFromBranchC {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, _round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(BRANCH_C_SIGNAL.for_one()))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        BRANCH_COMPLETED.end_branch(context, &round, "C".to_string())
    }
}

#[derive(Default)]
struct BlockingJoin;

impl Step for BlockingJoin {
    type Input = BlockingRoundJoin;

    fn wait_for(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(BRANCH_COMPLETED.for_branches(
            &input.join_round,
            input.blocking_round.required_signals,
        )))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let branches = BRANCH_COMPLETED.branch_results(context, &input.join_round)?;
        let round = input.blocking_round;
        let mut movements = vec![StepMovement::to(
            &ProcessRound,
            FiredBranchesRound {
                round: round.round,
                branches: sorted(branches),
            },
        )];
        if round.round < BLOCKING_JOIN_ROUND_COUNT {
            movements.push(StepMovement::to(
                &StartBlockingRound,
                BlockingJoinRound {
                    round: round.round + 1,
                    previous_round: Some(input.join_round),
                    ..round
                },
            ));
        }
        // The last round's late signals stay pending until the Flow closes.
        Ok(StepDecision::go_to_many(movements))
    }
}

#[derive(Default)]
pub struct ProcessRound;

impl Step for ProcessRound {
    type Input = FiredBranchesRound;

    fn execute(
        &self,
        _context: &mut Context,
        fired_round: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(fired_round))
    }
}

static CARRIER_A_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("carrierAReleased"));
static CARRIER_B_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("carrierBReleased"));
static CARRIER_C_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("carrierCReleased"));
static BID_RECEIVED: LazyLock<Join<String>> = LazyLock::new(|| Join::new("bidReceived"));
static CARRIER_BOOKING_CONFIRMED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("carrierBookingConfirmed"));

pub const RELEASE_CARRIER_BID: Rpc<String, ()> = Rpc::new("release_carrier_bid");
pub const GET_QUEUED_CARRIER_RELEASE_COUNT: Rpc<(), usize> =
    Rpc::new("get_queued_carrier_release_count");
pub const CONFIRM_CARRIER_BOOKING: Rpc<(), ()> = Rpc::new("confirm_carrier_booking");

/// The input of the join Step of the carrier bids.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CarrierBidJoin {
    pub required_bids: usize,
    pub join_round: JoinRound,
}

/// WCP-29 Cancelling Discriminator (`required_bids` = 1) and WCP-32
/// Cancelling Partial Join (`required_bids` = 2): the join cancels the
/// sibling branches that have not reported.
#[derive(Default)]
pub struct CancellingPartialJoinFlow {
    request_bids: RequestBids,
    bid_from_carrier_a: BidFromCarrierA,
    bid_from_carrier_b: BidFromCarrierB,
    bid_from_carrier_c: BidFromCarrierC,
    accept_bids: AcceptBids,
    confirm_carrier_booking: ConfirmCarrierBooking,
}

impl CancellingPartialJoinFlow {
    fn get_queued_carrier_release_count(
        &self,
        context: &mut Context,
    ) -> HandlerResult<RpcResult<usize>> {
        let queued_release_count = CARRIER_A_RELEASED.pending_messages(context)?.len()
            + CARRIER_B_RELEASED.pending_messages(context)?.len()
            + CARRIER_C_RELEASED.pending_messages(context)?.len();
        Ok(RpcResult::new(queued_release_count))
    }

    fn confirm_carrier_booking(&self, context: &mut Context) -> HandlerResult<()> {
        CARRIER_BOOKING_CONFIRMED.publish(context, "confirmed".to_string())
    }

    fn release_carrier_bid(&self, context: &mut Context, carrier: String) -> HandlerResult<()> {
        let channel = match carrier.as_str() {
            "A" => &CARRIER_A_RELEASED,
            "B" => &CARRIER_B_RELEASED,
            "C" => &CARRIER_C_RELEASED,
            _ => return Err(unknown_letter_error("InvalidCarrier", "carrier", &carrier)),
        };
        channel.publish(context, carrier)
    }
}

impl Flow for CancellingPartialJoinFlow {
    type StartInput = usize;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.request_bids)
            .and(&self.bid_from_carrier_a)
            .and(&self.bid_from_carrier_b)
            .and(&self.bid_from_carrier_c)
            .and(&self.accept_bids)
            .and(&self.confirm_carrier_booking)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&CARRIER_A_RELEASED)
            .channel(&CARRIER_B_RELEASED)
            .channel(&CARRIER_C_RELEASED)
            .join(&BID_RECEIVED)
            .channel(&CARRIER_BOOKING_CONFIRMED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(RELEASE_CARRIER_BID, Self::release_carrier_bid)
            .function_without_input(
                GET_QUEUED_CARRIER_RELEASE_COUNT
                    .load_channel(&CARRIER_A_RELEASED)
                    .load_channel(&CARRIER_B_RELEASED)
                    .load_channel(&CARRIER_C_RELEASED),
                Self::get_queued_carrier_release_count,
            )
            .procedure_without_input(CONFIRM_CARRIER_BOOKING, Self::confirm_carrier_booking)
    }
}

#[derive(Default)]
struct RequestBids;

impl Step for RequestBids {
    type Input = usize;

    fn execute(
        &self,
        context: &mut Context,
        required_bids: Self::Input,
    ) -> HandlerResult<StepDecision> {
        if !(1..=CARRIER_COUNT).contains(&required_bids) {
            return Ok(StepDecision::force_fail(format!(
                "required bids must be between 1 and {CARRIER_COUNT}, got {required_bids}"
            )));
        }
        let movements = BID_RECEIVED.fork(
            context,
            vec![
                round_branch(BidFromCarrierA),
                round_branch(BidFromCarrierB),
                round_branch(BidFromCarrierC),
            ],
            |join_round| {
                StepMovement::to(
                    &AcceptBids,
                    CarrierBidJoin {
                        required_bids,
                        join_round,
                    },
                )
            },
        )?;
        Ok(StepDecision::go_to_many(movements))
    }
}

#[derive(Default)]
pub struct BidFromCarrierA;

impl Step for BidFromCarrierA {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, _round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CARRIER_A_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        BID_RECEIVED.end_branch(context, &round, "A".to_string())
    }
}

#[derive(Default)]
struct BidFromCarrierB;

impl Step for BidFromCarrierB {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, _round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CARRIER_B_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        BID_RECEIVED.end_branch(context, &round, "B".to_string())
    }
}

#[derive(Default)]
struct BidFromCarrierC;

impl Step for BidFromCarrierC {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, _round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CARRIER_C_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        BID_RECEIVED.end_branch(context, &round, "C".to_string())
    }
}

#[derive(Default)]
pub struct AcceptBids;

impl Step for AcceptBids {
    type Input = CarrierBidJoin;

    fn wait_for(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(
            BID_RECEIVED.for_branches(&input.join_round, input.required_bids),
        ))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let accepted_bids = BID_RECEIVED.branch_results(context, &input.join_round)?;
        Ok(
            StepDecision::go_to(&ConfirmCarrierBooking, sorted(accepted_bids))
                .cancel_sibling_step(&BidFromCarrierA)
                .cancel_sibling_step(&BidFromCarrierB)
                .cancel_sibling_step(&BidFromCarrierC),
        )
    }
}

#[derive(Default)]
struct ConfirmCarrierBooking;

impl Step for ConfirmCarrierBooking {
    type Input = Vec<String>;

    fn wait_for(&self, _context: &mut Context, _accepted_bids: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CARRIER_BOOKING_CONFIRMED.for_one()))
    }

    fn execute(
        &self,
        _context: &mut Context,
        accepted_bids: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(accepted_bids))
    }
}

/// Returns the input error for a letter outside A, B, and C.
fn unknown_letter_error(error_type: &str, subject: &str, letter: &str) -> HandlerError {
    HandlerError::new(
        error_type,
        format!("{subject} must be \"A\", \"B\", or \"C\", got {letter:?}"),
    )
}
