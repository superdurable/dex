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
    Channel, ConditionCombination, Context, Flow, HandlerError, HandlerResult, PersistenceSchema,
    Rpc, RpcList, Step, StepDecision, StepList, StepMovement, Wait,
};
use serde::{Deserialize, Serialize};

use super::sorted;

const SUPPLIER_COUNT: usize = 3;
const CARRIER_COUNT: usize = 3;
const BLOCKING_JOIN_ROUND_COUNT: u32 = 2;
const BRANCH_LETTERS: [&str; 3] = ["A", "B", "C"];

static SUPPLIER_A_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("supplier_a_released"));
static SUPPLIER_B_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("supplier_b_released"));
static SUPPLIER_C_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("supplier_c_released"));
static QUOTE_RECEIVED: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("quote_received"));

pub const RELEASE_SUPPLIER_QUOTE: Rpc<String, ()> = Rpc::new("release_supplier_quote");

/// WCP-9 Structured Discriminator (`required_quotes` = 1) and WCP-30
/// Structured Partial Join (`required_quotes` = 2). A reset Step absorbs
/// the remaining quotes so the join fires only once.
#[derive(Default)]
pub struct StructuredPartialJoinFlow {
    request_quotes: RequestQuotes,
    quote_from_supplier_a: QuoteFromSupplierA,
    quote_from_supplier_b: QuoteFromSupplierB,
    quote_from_supplier_c: QuoteFromSupplierC,
    accept_quotes: AcceptQuotes,
    place_order: PlaceOrder,
    reset_quote_join: ResetQuoteJoin,
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
    type StartInput = usize;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.request_quotes)
            .and(&self.quote_from_supplier_a)
            .and(&self.quote_from_supplier_b)
            .and(&self.quote_from_supplier_c)
            .and(&self.accept_quotes)
            .and(&self.place_order)
            .and(&self.reset_quote_join)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&SUPPLIER_A_RELEASED)
            .channel(&SUPPLIER_B_RELEASED)
            .channel(&SUPPLIER_C_RELEASED)
            .channel(&QUOTE_RECEIVED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(RELEASE_SUPPLIER_QUOTE, Self::release_supplier_quote)
    }
}

#[derive(Default)]
struct RequestQuotes;

impl Step for RequestQuotes {
    type Input = usize;

    fn execute(
        &self,
        _context: &mut Context,
        required_quotes: Self::Input,
    ) -> HandlerResult<StepDecision> {
        if !(1..=SUPPLIER_COUNT).contains(&required_quotes) {
            return Ok(StepDecision::force_fail(format!(
                "required quotes must be between 1 and {SUPPLIER_COUNT}, got {required_quotes}"
            )));
        }
        Ok(StepDecision::go_to_many([
            StepMovement::to(&QuoteFromSupplierA, ()),
            StepMovement::to(&QuoteFromSupplierB, ()),
            StepMovement::to(&QuoteFromSupplierC, ()),
            StepMovement::to(&AcceptQuotes, required_quotes),
        ]))
    }
}

#[derive(Default)]
pub struct QuoteFromSupplierA;

impl Step for QuoteFromSupplierA {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(SUPPLIER_A_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        QUOTE_RECEIVED.publish(context, "A".to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct QuoteFromSupplierB;

impl Step for QuoteFromSupplierB {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(SUPPLIER_B_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        QUOTE_RECEIVED.publish(context, "B".to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct QuoteFromSupplierC;

impl Step for QuoteFromSupplierC {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(SUPPLIER_C_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        QUOTE_RECEIVED.publish(context, "C".to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct AcceptQuotes;

impl Step for AcceptQuotes {
    type Input = usize;

    fn wait_for(
        &self,
        _context: &mut Context,
        required_quotes: Self::Input,
    ) -> HandlerResult<Wait> {
        Ok(Wait::until(QUOTE_RECEIVED.for_n(required_quotes)))
    }

    fn execute(
        &self,
        context: &mut Context,
        required_quotes: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let accepted_quotes = QUOTE_RECEIVED.condition_results(context)?;
        Ok(StepDecision::go_to_many([
            StepMovement::to(&PlaceOrder, accepted_quotes),
            StepMovement::to(
                &ResetQuoteJoin,
                SUPPLIER_COUNT.saturating_sub(required_quotes),
            ),
        ]))
    }
}

#[derive(Default)]
pub struct PlaceOrder;

impl Step for PlaceOrder {
    type Input = Vec<String>;

    fn execute(
        &self,
        _context: &mut Context,
        accepted_quotes: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(sorted(accepted_quotes)))
    }
}

#[derive(Default)]
struct ResetQuoteJoin;

impl Step for ResetQuoteJoin {
    type Input = usize;

    fn wait_for(
        &self,
        _context: &mut Context,
        remaining_quotes: Self::Input,
    ) -> HandlerResult<Wait> {
        Ok(Wait::until(QUOTE_RECEIVED.for_n(remaining_quotes)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _remaining_quotes: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let ignored_quotes = QUOTE_RECEIVED.condition_results(context)?;
        Ok(StepDecision::graceful_complete(sorted(ignored_quotes)))
    }
}

static BRANCH_A_SIGNAL: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("branch_a_signal"));
static BRANCH_B_SIGNAL: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("branch_b_signal"));
static BRANCH_C_SIGNAL: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("branch_c_signal"));

pub const SEND_BRANCH_SIGNAL: Rpc<String, ()> = Rpc::new("send_branch_signal");

/// One firing of the blocking join.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BlockingJoinRound {
    pub required_signals: usize,
    pub round: u32,
}

/// The branches that fired the blocking join in one round.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FiredBranchesRound {
    pub round: u32,
    pub branches: Vec<String>,
}

/// The branches the reset Step must absorb before the join can fire again.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BlockingJoinReset {
    pub required_signals: usize,
    pub round: u32,
    pub pending_branches: Vec<String>,
}

/// WCP-28 Blocking Discriminator (`required_signals` = 1) and WCP-31
/// Blocking Partial Join (`required_signals` = 2). Signals that arrive
/// before the reset stay queued for the next round.
#[derive(Default)]
pub struct BlockingPartialJoinFlow {
    blocking_join: BlockingJoin,
    process_round: ProcessRound,
    reset_blocking_join: ResetBlockingJoin,
}

impl BlockingPartialJoinFlow {
    fn send_branch_signal(&self, context: &mut Context, branch: String) -> HandlerResult<()> {
        let channel = branch_signal_channel(&branch)
            .ok_or_else(|| unknown_letter_error("InvalidBranch", "branch", &branch))?;
        channel.publish(context, branch)
    }
}

impl Flow for BlockingPartialJoinFlow {
    type StartInput = BlockingJoinRound;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.blocking_join)
            .and(&self.process_round)
            .and(&self.reset_blocking_join)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&BRANCH_A_SIGNAL)
            .channel(&BRANCH_B_SIGNAL)
            .channel(&BRANCH_C_SIGNAL)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(SEND_BRANCH_SIGNAL, Self::send_branch_signal)
    }
}

#[derive(Default)]
struct BlockingJoin;

impl Step for BlockingJoin {
    type Input = BlockingJoinRound;

    fn wait_for(&self, _context: &mut Context, round: Self::Input) -> HandlerResult<Wait> {
        match round.required_signals {
            1 => Ok(Wait::any_of([
                BRANCH_A_SIGNAL.for_one(),
                BRANCH_B_SIGNAL.for_one(),
                BRANCH_C_SIGNAL.for_one(),
            ])),
            2 => Ok(Wait::any_combination_of([
                ConditionCombination::all_of([
                    BRANCH_A_SIGNAL.for_one().with_id("ab-a"),
                    BRANCH_B_SIGNAL.for_one().with_id("ab-b"),
                ]),
                ConditionCombination::all_of([
                    BRANCH_A_SIGNAL.for_one().with_id("ac-a"),
                    BRANCH_C_SIGNAL.for_one().with_id("ac-c"),
                ]),
                ConditionCombination::all_of([
                    BRANCH_B_SIGNAL.for_one().with_id("bc-b"),
                    BRANCH_C_SIGNAL.for_one().with_id("bc-c"),
                ]),
            ])),
            other => Err(HandlerError::new(
                "InvalidRequiredSignals",
                format!("required signals must be 1 or 2, got {other}"),
            )),
        }
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        let mut fired_branches = Vec::new();
        let mut pending_branches = Vec::new();
        for letter in BRANCH_LETTERS {
            let channel = branch_signal_channel(letter)
                .ok_or_else(|| unknown_letter_error("InvalidBranch", "branch", letter))?;
            if channel.condition_results(context)?.is_empty() {
                pending_branches.push(letter.to_string());
            } else {
                fired_branches.push(letter.to_string());
            }
        }
        Ok(StepDecision::go_to_many([
            StepMovement::to(
                &ProcessRound,
                FiredBranchesRound {
                    round: round.round,
                    branches: fired_branches,
                },
            ),
            StepMovement::to(
                &ResetBlockingJoin,
                BlockingJoinReset {
                    required_signals: round.required_signals,
                    round: round.round,
                    pending_branches,
                },
            ),
        ]))
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

#[derive(Default)]
struct ResetBlockingJoin;

impl Step for ResetBlockingJoin {
    type Input = BlockingJoinReset;

    fn wait_for(&self, _context: &mut Context, reset: Self::Input) -> HandlerResult<Wait> {
        let conditions = reset
            .pending_branches
            .iter()
            .map(|letter| {
                branch_signal_channel(letter)
                    .map(Channel::for_one)
                    .ok_or_else(|| unknown_letter_error("InvalidBranch", "branch", letter))
            })
            .collect::<HandlerResult<Vec<_>>>()?;
        Ok(Wait::all_of(conditions))
    }

    fn execute(&self, _context: &mut Context, reset: Self::Input) -> HandlerResult<StepDecision> {
        if reset.round < BLOCKING_JOIN_ROUND_COUNT {
            return Ok(StepDecision::go_to(
                &BlockingJoin,
                BlockingJoinRound {
                    required_signals: reset.required_signals,
                    round: reset.round + 1,
                },
            ));
        }
        Ok(StepDecision::graceful_complete(reset.round))
    }
}

fn branch_signal_channel(letter: &str) -> Option<&'static Channel<String>> {
    match letter {
        "A" => Some(LazyLock::force(&BRANCH_A_SIGNAL)),
        "B" => Some(LazyLock::force(&BRANCH_B_SIGNAL)),
        "C" => Some(LazyLock::force(&BRANCH_C_SIGNAL)),
        _ => None,
    }
}

static CARRIER_A_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("carrier_a_released"));
static CARRIER_B_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("carrier_b_released"));
static CARRIER_C_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("carrier_c_released"));
static BID_RECEIVED: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("bid_received"));

pub const RELEASE_CARRIER_BID: Rpc<String, ()> = Rpc::new("release_carrier_bid");

/// WCP-29 Cancelling Discriminator (`required_bids` = 1) and WCP-32
/// Cancelling Partial Join (`required_bids` = 2): the join cancels the
/// branches that have not reported.
#[derive(Default)]
pub struct CancellingPartialJoinFlow {
    request_bids: RequestBids,
    bid_from_carrier_a: BidFromCarrierA,
    bid_from_carrier_b: BidFromCarrierB,
    bid_from_carrier_c: BidFromCarrierC,
    accept_bids: AcceptBids,
}

impl CancellingPartialJoinFlow {
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
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&CARRIER_A_RELEASED)
            .channel(&CARRIER_B_RELEASED)
            .channel(&CARRIER_C_RELEASED)
            .channel(&BID_RECEIVED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(RELEASE_CARRIER_BID, Self::release_carrier_bid)
    }
}

#[derive(Default)]
struct RequestBids;

impl Step for RequestBids {
    type Input = usize;

    fn execute(
        &self,
        _context: &mut Context,
        required_bids: Self::Input,
    ) -> HandlerResult<StepDecision> {
        if !(1..=CARRIER_COUNT).contains(&required_bids) {
            return Ok(StepDecision::force_fail(format!(
                "required bids must be between 1 and {CARRIER_COUNT}, got {required_bids}"
            )));
        }
        Ok(StepDecision::go_to_many([
            StepMovement::to(&BidFromCarrierA, ()),
            StepMovement::to(&BidFromCarrierB, ()),
            StepMovement::to(&BidFromCarrierC, ()),
            StepMovement::to(&AcceptBids, required_bids),
        ]))
    }
}

#[derive(Default)]
pub struct BidFromCarrierA;

impl Step for BidFromCarrierA {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CARRIER_A_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        BID_RECEIVED.publish(context, "A".to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct BidFromCarrierB;

impl Step for BidFromCarrierB {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CARRIER_B_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        BID_RECEIVED.publish(context, "B".to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct BidFromCarrierC;

impl Step for BidFromCarrierC {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CARRIER_C_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        BID_RECEIVED.publish(context, "C".to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct AcceptBids;

impl Step for AcceptBids {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, required_bids: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(BID_RECEIVED.for_n(required_bids)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _required_bids: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let accepted_bids = BID_RECEIVED.condition_results(context)?;
        Ok(StepDecision::graceful_complete(sorted(accepted_bids))
            .cancel_step(&BidFromCarrierA)
            .cancel_step(&BidFromCarrierB)
            .cancel_step(&BidFromCarrierC))
    }
}

/// Returns the input error for a letter outside A, B, and C.
fn unknown_letter_error(error_type: &str, subject: &str, letter: &str) -> HandlerError {
    HandlerError::new(
        error_type,
        format!("{subject} must be \"A\", \"B\", or \"C\", got {letter:?}"),
    )
}
