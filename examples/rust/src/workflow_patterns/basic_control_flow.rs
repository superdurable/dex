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

//! Basic control flow: Sequence (WCP-1), Parallel Split (WCP-2),
//! Synchronization (WCP-3), Exclusive Choice (WCP-4), and Simple Merge (WCP-5).

use std::sync::LazyLock;

use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step, StepDecision,
    StepList, StepMovement, Wait,
};

use super::{extend_trail, sorted};

/// Claims at or above this amount need a manual review.
pub const MANUAL_REVIEW_CLAIM_AMOUNT: i64 = 1000;

/// WCP-1 Sequence: each Step moves to the next one with GoTo.
#[derive(Default)]
pub struct SequenceFlow {
    receive_order: ReceiveOrder,
    check_inventory: CheckInventory,
    ship_order: ShipOrder,
}

impl Flow for SequenceFlow {
    type StartInput = Vec<String>;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.receive_order)
            .and(&self.check_inventory)
            .and(&self.ship_order)
    }
}

#[derive(Default)]
struct ReceiveOrder;

impl Step for ReceiveOrder {
    type Input = Vec<String>;

    fn execute(&self, _context: &mut Context, trail: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &CheckInventory,
            extend_trail(trail, self.step_type()),
        ))
    }
}

#[derive(Default)]
struct CheckInventory;

impl Step for CheckInventory {
    type Input = Vec<String>;

    fn execute(&self, _context: &mut Context, trail: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &ShipOrder,
            extend_trail(trail, self.step_type()),
        ))
    }
}

#[derive(Default)]
struct ShipOrder;

impl Step for ShipOrder {
    type Input = Vec<String>;

    fn execute(&self, _context: &mut Context, trail: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(extend_trail(
            trail,
            self.step_type(),
        )))
    }
}

static FULFILLMENT_BRANCH_COMPLETED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("fulfillment_branch_completed"));
static PAYMENT_APPROVED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("payment_approved"));

pub const APPROVE_PAYMENT: Rpc<(), ()> = Rpc::new("approve_payment");

const FULFILLMENT_BRANCH_COUNT: usize = 2;

/// WCP-2 Parallel Split and WCP-3 Synchronization: GoToMany starts both
/// branches, and the join waits for one Channel message per branch.
#[derive(Default)]
pub struct ParallelSplitSynchronizationFlow {
    split_order: SplitOrder,
    pack_items: PackItems,
    charge_payment: ChargePayment,
    synchronize_fulfillment: SynchronizeFulfillment,
}

impl ParallelSplitSynchronizationFlow {
    fn approve_payment(&self, context: &mut Context) -> HandlerResult<()> {
        PAYMENT_APPROVED.publish(context, "approved".to_string())
    }
}

impl Flow for ParallelSplitSynchronizationFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.split_order)
            .and(&self.pack_items)
            .and(&self.charge_payment)
            .and(&self.synchronize_fulfillment)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&FULFILLMENT_BRANCH_COMPLETED)
            .channel(&PAYMENT_APPROVED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(APPROVE_PAYMENT, Self::approve_payment)
    }
}

#[derive(Default)]
struct SplitOrder;

impl Step for SplitOrder {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&PackItems, ()),
            StepMovement::to(&ChargePayment, ()),
            StepMovement::to(&SynchronizeFulfillment, ()),
        ]))
    }
}

#[derive(Default)]
pub struct PackItems;

impl Step for PackItems {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        FULFILLMENT_BRANCH_COMPLETED.publish(context, self.step_type().to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct ChargePayment;

impl Step for ChargePayment {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(PAYMENT_APPROVED.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        FULFILLMENT_BRANCH_COMPLETED.publish(context, self.step_type().to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct SynchronizeFulfillment;

impl Step for SynchronizeFulfillment {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(
            FULFILLMENT_BRANCH_COMPLETED.for_n(FULFILLMENT_BRANCH_COUNT),
        ))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let branches = FULFILLMENT_BRANCH_COMPLETED.condition_results(context)?;
        Ok(StepDecision::graceful_complete(sorted(branches)))
    }
}

/// WCP-4 Exclusive Choice and WCP-5 Simple Merge: one branch runs, and
/// both branches move to the same closing Step type.
#[derive(Default)]
pub struct ExclusiveChoiceSimpleMergeFlow {
    route_claim: RouteClaim,
    manual_review: ManualReview,
    auto_approve: AutoApprove,
    close_claim: CloseClaim,
}

impl Flow for ExclusiveChoiceSimpleMergeFlow {
    type StartInput = i64;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.route_claim)
            .and(&self.manual_review)
            .and(&self.auto_approve)
            .and(&self.close_claim)
    }
}

#[derive(Default)]
struct RouteClaim;

impl Step for RouteClaim {
    type Input = i64;

    fn execute(
        &self,
        _context: &mut Context,
        claim_amount: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let trail = vec![self.step_type().to_string()];
        if claim_amount >= MANUAL_REVIEW_CLAIM_AMOUNT {
            return Ok(StepDecision::go_to(&ManualReview, trail));
        }
        Ok(StepDecision::go_to(&AutoApprove, trail))
    }
}

#[derive(Default)]
struct ManualReview;

impl Step for ManualReview {
    type Input = Vec<String>;

    fn execute(&self, _context: &mut Context, trail: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &CloseClaim,
            extend_trail(trail, self.step_type()),
        ))
    }
}

#[derive(Default)]
struct AutoApprove;

impl Step for AutoApprove {
    type Input = Vec<String>;

    fn execute(&self, _context: &mut Context, trail: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &CloseClaim,
            extend_trail(trail, self.step_type()),
        ))
    }
}

#[derive(Default)]
struct CloseClaim;

impl Step for CloseClaim {
    type Input = Vec<String>;

    fn execute(&self, _context: &mut Context, trail: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(extend_trail(
            trail,
            self.step_type(),
        )))
    }
}
