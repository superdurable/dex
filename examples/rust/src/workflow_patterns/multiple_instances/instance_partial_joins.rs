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

//! Partial joins for multiple instances: WCP-34 to WCP-36.

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeMap, Channel, ChannelMap, Context, Flow, HandlerResult, PersistenceSchema,
    Rpc, RpcList, RpcResult, Step, StepDecision, StepList, StepMovement, StepOptions, Wait,
};
use serde::{Deserialize, Serialize};

use crate::workflow_patterns::{round_instance, sorted};

/// One round of the static partial join.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InspectionRound {
    pub inspection_count: usize,
    pub required_inspections: usize,
    pub round: u32,
    pub round_count: u32,
}

/// One inspection instance of a round.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UnitInspection {
    pub round: u32,
    pub unit_index: usize,
}

/// The unit indexes that one Step of a round consumed.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InspectedUnits {
    pub round: u32,
    pub unit_indexes: Vec<usize>,
}

static INSPECTION_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("inspectionReleased"));
static UNIT_INSPECTED: LazyLock<ChannelMap<usize>> =
    LazyLock::new(|| ChannelMap::new("unitInspected"));

pub const RELEASE_INSPECTIONS: Rpc<usize, ()> = Rpc::new("release_inspections");

/// WCP-34 Static Partial Join for Multiple Instances: the join fires after
/// the required instances of a round, and a reset Step absorbs the rest
/// before the next round re-enables the join.
#[derive(Default)]
pub struct StaticPartialJoinForMultipleInstancesFlow {
    start_inspections: StartInspections,
    inspect_unit: InspectUnit,
    accept_inspections: AcceptInspections,
    approve_batch: ApproveBatch,
    reset_inspection_join: ResetInspectionJoin,
}

impl StaticPartialJoinForMultipleInstancesFlow {
    fn release_inspections(&self, context: &mut Context, count: usize) -> HandlerResult<()> {
        for _ in 0..count {
            INSPECTION_RELEASED.publish(context, "released".to_string())?;
        }
        Ok(())
    }
}

impl Flow for StaticPartialJoinForMultipleInstancesFlow {
    type StartInput = InspectionRound;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_inspections)
            .and(&self.inspect_unit)
            .and(&self.accept_inspections)
            .and(&self.approve_batch)
            .and(&self.reset_inspection_join)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&INSPECTION_RELEASED)
            .channel_map(&UNIT_INSPECTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(RELEASE_INSPECTIONS, Self::release_inspections)
    }
}

#[derive(Default)]
pub struct StartInspections;

impl Step for StartInspections {
    type Input = InspectionRound;

    fn execute(&self, _context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        if round.required_inspections > round.inspection_count {
            return Ok(StepDecision::force_fail(
                "required inspections exceed the inspection count",
            ));
        }
        let inspections = (0..round.inspection_count).map(|unit_index| {
            StepMovement::to(
                &InspectUnit,
                UnitInspection {
                    round: round.round,
                    unit_index,
                },
            )
        });
        let join = StepMovement::to(&AcceptInspections, round.clone());
        Ok(StepDecision::go_to_many(inspections.chain([join])))
    }
}

#[derive(Default)]
struct InspectUnit;

impl Step for InspectUnit {
    type Input = UnitInspection;

    fn wait_for(&self, _context: &mut Context, _inspection: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(INSPECTION_RELEASED.for_one()))
    }

    fn execute(
        &self,
        context: &mut Context,
        inspection: Self::Input,
    ) -> HandlerResult<StepDecision> {
        UNIT_INSPECTED.publish(
            context,
            &round_instance(inspection.round),
            inspection.unit_index,
        )?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct AcceptInspections;

impl Step for AcceptInspections {
    type Input = InspectionRound;

    fn wait_for(&self, _context: &mut Context, round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(UNIT_INSPECTED.for_n(
            &round_instance(round.round),
            round.required_inspections,
        )))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        let inspected_units = InspectedUnits {
            round: round.round,
            unit_indexes: sorted(
                UNIT_INSPECTED.condition_results(context, &round_instance(round.round))?,
            ),
        };
        Ok(StepDecision::go_to_many([
            StepMovement::to(&ApproveBatch, inspected_units),
            StepMovement::to(&ResetInspectionJoin, round),
        ]))
    }
}

#[derive(Default)]
pub struct ApproveBatch;

impl Step for ApproveBatch {
    type Input = InspectedUnits;

    fn execute(
        &self,
        _context: &mut Context,
        inspected_units: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(inspected_units))
    }
}

#[derive(Default)]
struct ResetInspectionJoin;

impl Step for ResetInspectionJoin {
    type Input = InspectionRound;

    fn wait_for(&self, _context: &mut Context, round: Self::Input) -> HandlerResult<Wait> {
        let remaining_inspections = round.inspection_count - round.required_inspections;
        Ok(Wait::until(UNIT_INSPECTED.for_n(
            &round_instance(round.round),
            remaining_inspections,
        )))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        let unit_indexes =
            sorted(UNIT_INSPECTED.condition_results(context, &round_instance(round.round))?);
        if round.round < round.round_count {
            return Ok(StepDecision::go_to(
                &StartInspections,
                InspectionRound {
                    round: round.round + 1,
                    ..round
                },
            ));
        }
        Ok(StepDecision::graceful_complete(InspectedUnits {
            round: round.round,
            unit_indexes,
        }))
    }
}

/// How many inspection instances to start, and how many complete the join.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InspectionPlan {
    pub inspection_count: usize,
    pub required_inspections: usize,
}

static QUORUM_INSPECTION_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("quorumInspectionReleased"));
static QUORUM_UNIT_INSPECTED: LazyLock<Channel<usize>> =
    LazyLock::new(|| Channel::new("quorumUnitInspected"));
static QUORUM_CERTIFIED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("quorumCertified"));

pub const RELEASE_QUORUM_INSPECTIONS: Rpc<usize, ()> = Rpc::new("release_quorum_inspections");
pub const GET_QUEUED_QUORUM_RELEASE_COUNT: Rpc<(), usize> =
    Rpc::new("get_queued_quorum_release_count");
pub const CERTIFY_QUORUM: Rpc<(), ()> = Rpc::new("certify_quorum");

/// WCP-35 Cancelling Partial Join for Multiple Instances: the join cancels
/// the instances that have not reported.
#[derive(Default)]
pub struct CancellingPartialJoinForMultipleInstancesFlow {
    start_quorum_inspections: StartQuorumInspections,
    inspect_quorum_unit: InspectQuorumUnit,
    accept_quorum_inspections: AcceptQuorumInspections,
    certify_quorum: CertifyQuorum,
}

impl CancellingPartialJoinForMultipleInstancesFlow {
    fn release_quorum_inspections(&self, context: &mut Context, count: usize) -> HandlerResult<()> {
        for _ in 0..count {
            QUORUM_INSPECTION_RELEASED.publish(context, "released".to_string())?;
        }
        Ok(())
    }

    fn get_queued_quorum_release_count(
        &self,
        context: &mut Context,
    ) -> HandlerResult<RpcResult<usize>> {
        let queued_releases = QUORUM_INSPECTION_RELEASED.pending_messages(context)?;
        Ok(RpcResult::new(queued_releases.len()))
    }

    fn certify_quorum(&self, context: &mut Context) -> HandlerResult<()> {
        QUORUM_CERTIFIED.publish(context, "certified".to_string())
    }
}

impl Flow for CancellingPartialJoinForMultipleInstancesFlow {
    type StartInput = InspectionPlan;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_quorum_inspections)
            .and(&self.inspect_quorum_unit)
            .and(&self.accept_quorum_inspections)
            .and(&self.certify_quorum)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&QUORUM_INSPECTION_RELEASED)
            .channel(&QUORUM_UNIT_INSPECTED)
            .channel(&QUORUM_CERTIFIED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(RELEASE_QUORUM_INSPECTIONS, Self::release_quorum_inspections)
            .function_without_input(
                GET_QUEUED_QUORUM_RELEASE_COUNT.load_channel(&QUORUM_INSPECTION_RELEASED),
                Self::get_queued_quorum_release_count,
            )
            .procedure_without_input(CERTIFY_QUORUM, Self::certify_quorum)
    }
}

#[derive(Default)]
struct StartQuorumInspections;

impl Step for StartQuorumInspections {
    type Input = InspectionPlan;

    fn execute(&self, _context: &mut Context, plan: Self::Input) -> HandlerResult<StepDecision> {
        let inspections =
            (0..plan.inspection_count).map(|index| StepMovement::to(&InspectQuorumUnit, index));
        Ok(StepDecision::go_to_many(inspections.chain([
            StepMovement::to(&AcceptQuorumInspections, plan.required_inspections),
        ])))
    }
}

#[derive(Default)]
struct InspectQuorumUnit;

impl Step for InspectQuorumUnit {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, _index: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(QUORUM_INSPECTION_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, index: Self::Input) -> HandlerResult<StepDecision> {
        QUORUM_UNIT_INSPECTED.publish(context, index)?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
pub struct AcceptQuorumInspections;

impl Step for AcceptQuorumInspections {
    type Input = usize;

    fn wait_for(
        &self,
        _context: &mut Context,
        required_inspections: Self::Input,
    ) -> HandlerResult<Wait> {
        Ok(Wait::until(
            QUORUM_UNIT_INSPECTED.for_n(required_inspections),
        ))
    }

    fn execute(
        &self,
        context: &mut Context,
        _required_inspections: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let inspected_units = QUORUM_UNIT_INSPECTED.condition_results(context)?;
        Ok(StepDecision::go_to(&CertifyQuorum, sorted(inspected_units))
            .cancel_step(&InspectQuorumUnit))
    }
}

#[derive(Default)]
struct CertifyQuorum;

impl Step for CertifyQuorum {
    type Input = Vec<usize>;

    fn wait_for(&self, _context: &mut Context, _unit_indexes: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(QUORUM_CERTIFIED.for_one()))
    }

    fn execute(
        &self,
        _context: &mut Context,
        unit_indexes: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(unit_indexes))
    }
}

/// One evaluated bid.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BidEvaluation {
    pub amount: i64,
    pub is_accepted: bool,
}

/// The progress of the dynamic partial join.
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BidJoinProgress {
    pub required_accepted_bids: usize,
    pub accepted_bids: Vec<i64>,
}

/// Bids at or below this amount are accepted.
pub const MAXIMUM_ACCEPTED_BID_AMOUNT: i64 = 1000;

pub static IS_BID_ADMISSION_CLOSED: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("isBidAdmissionClosed"));
pub static EVALUATION_STARTED_BIDS: LazyLock<AttributeMap<bool>> =
    LazyLock::new(|| AttributeMap::new("evaluationStartedBids"));
static BID_EVALUATED: LazyLock<Channel<BidEvaluation>> =
    LazyLock::new(|| Channel::new("bidEvaluated"));
static BID_GATE_OPENED: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("bidGateOpened"));
static CONTRACT_SIGNED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("contractSigned"));

pub const ADD_BID: Rpc<i64, bool> = Rpc::new("add_bid");
pub const GET_STARTED_EVALUATION_COUNT: Rpc<(), usize> = Rpc::new("get_started_evaluation_count");
pub const OPEN_BID_GATE: Rpc<usize, ()> = Rpc::new("open_bid_gate");
pub const SIGN_CONTRACT: Rpc<(), ()> = Rpc::new("sign_contract");

/// WCP-36 Dynamic Partial Join for Multiple Instances: RPCs add instances,
/// and the join closes admission once enough bids are accepted.
#[derive(Default)]
pub struct DynamicPartialJoinForMultipleInstancesFlow {
    join_bids: JoinBids,
    evaluate_bid: EvaluateBid,
    award_contract: AwardContract,
}

impl DynamicPartialJoinForMultipleInstancesFlow {
    fn add_bid(&self, context: &mut Context, amount: i64) -> HandlerResult<RpcResult<bool>> {
        if IS_BID_ADMISSION_CLOSED.get(context)?.unwrap_or(false) {
            return Ok(RpcResult::new(false));
        }
        Ok(RpcResult::new(true).then(StepMovement::to(&EvaluateBid, amount)))
    }

    fn get_started_evaluation_count(
        &self,
        context: &mut Context,
    ) -> HandlerResult<RpcResult<usize>> {
        Ok(RpcResult::new(EVALUATION_STARTED_BIDS.map_size(context)?))
    }

    fn open_bid_gate(&self, context: &mut Context, count: usize) -> HandlerResult<()> {
        for _ in 0..count {
            BID_GATE_OPENED.publish(context, "opened".to_string())?;
        }
        Ok(())
    }

    fn sign_contract(&self, context: &mut Context) -> HandlerResult<()> {
        CONTRACT_SIGNED.publish(context, "signed".to_string())
    }
}

impl Flow for DynamicPartialJoinForMultipleInstancesFlow {
    type StartInput = BidJoinProgress;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.join_bids)
            .and(&self.evaluate_bid)
            .and(&self.award_contract)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&IS_BID_ADMISSION_CLOSED)
            .attribute_map(&EVALUATION_STARTED_BIDS)
            .channel(&BID_EVALUATED)
            .channel(&BID_GATE_OPENED)
            .channel(&CONTRACT_SIGNED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(ADD_BID.lock(IS_BID_ADMISSION_CLOSED.lock()), Self::add_bid)
            .function_without_input(
                GET_STARTED_EVALUATION_COUNT.load_attribute_map(&EVALUATION_STARTED_BIDS),
                Self::get_started_evaluation_count,
            )
            .procedure(OPEN_BID_GATE, Self::open_bid_gate)
            .procedure_without_input(SIGN_CONTRACT, Self::sign_contract)
    }
}

#[derive(Default)]
struct JoinBids;

impl Step for JoinBids {
    type Input = BidJoinProgress;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(IS_BID_ADMISSION_CLOSED.lock())
    }

    fn wait_for(&self, _context: &mut Context, _progress: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(BID_EVALUATED.for_one()))
    }

    fn execute(
        &self,
        context: &mut Context,
        mut progress: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let accepted_amounts = BID_EVALUATED
            .condition_results(context)?
            .into_iter()
            .filter(|evaluation| evaluation.is_accepted)
            .map(|evaluation| evaluation.amount);
        progress.accepted_bids.extend(accepted_amounts);
        if progress.accepted_bids.len() >= progress.required_accepted_bids {
            IS_BID_ADMISSION_CLOSED.set(context, true)?;
            return Ok(StepDecision::go_to(&AwardContract, progress.accepted_bids));
        }
        Ok(StepDecision::go_to(&JoinBids, progress))
    }
}

#[derive(Default)]
struct EvaluateBid;

impl Step for EvaluateBid {
    type Input = i64;

    /// Marks the instance started, then holds it at the concurrency gate.
    fn wait_for(&self, context: &mut Context, amount: Self::Input) -> HandlerResult<Wait> {
        EVALUATION_STARTED_BIDS.set(context, &amount.to_string(), true)?;
        Ok(Wait::until(BID_GATE_OPENED.for_one()))
    }

    fn execute(&self, context: &mut Context, amount: Self::Input) -> HandlerResult<StepDecision> {
        BID_EVALUATED.publish(
            context,
            BidEvaluation {
                amount,
                is_accepted: amount <= MAXIMUM_ACCEPTED_BID_AMOUNT,
            },
        )?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct AwardContract;

impl Step for AwardContract {
    type Input = Vec<i64>;

    fn wait_for(&self, _context: &mut Context, _accepted_bids: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CONTRACT_SIGNED.for_one()))
    }

    fn execute(
        &self,
        _context: &mut Context,
        accepted_bids: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(sorted(accepted_bids)))
    }
}
