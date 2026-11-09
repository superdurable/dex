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

//! Multiple instances: WCP-12 to WCP-15, and the multiple-instance partial
//! joins WCP-34 to WCP-36.

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult,
    Step, StepDecision, StepList, StepMovement, StepOptions, Wait,
};
use serde::{Deserialize, Serialize};

use super::sorted;

static INVITATION_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("invitationReleased"));

pub const RELEASE_INVITATIONS: Rpc<usize, ()> = Rpc::new("release_invitations");

/// WCP-12 Multiple Instances without Synchronization: nothing waits for the
/// invitation instances, and the Flow completes once they all finish.
#[derive(Default)]
pub struct MultipleInstancesWithoutSynchronizationFlow {
    register_attendees: RegisterAttendees,
    send_invitation: SendInvitation,
    confirm_registration: ConfirmRegistration,
}

impl MultipleInstancesWithoutSynchronizationFlow {
    fn release_invitations(&self, context: &mut Context, count: usize) -> HandlerResult<()> {
        for _ in 0..count {
            INVITATION_RELEASED.publish(context, "released".to_string())?;
        }
        Ok(())
    }
}

impl Flow for MultipleInstancesWithoutSynchronizationFlow {
    type StartInput = Vec<String>;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.register_attendees)
            .and(&self.send_invitation)
            .and(&self.confirm_registration)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&INVITATION_RELEASED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(RELEASE_INVITATIONS, Self::release_invitations)
    }
}

#[derive(Default)]
struct RegisterAttendees;

impl Step for RegisterAttendees {
    type Input = Vec<String>;

    fn execute(
        &self,
        _context: &mut Context,
        attendees: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let invitations = attendees
            .into_iter()
            .map(|attendee| StepMovement::to(&SendInvitation, attendee));
        Ok(StepDecision::go_to_many(
            invitations.chain([StepMovement::to(&ConfirmRegistration, ())]),
        ))
    }
}

#[derive(Default)]
struct SendInvitation;

impl Step for SendInvitation {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _attendee: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(INVITATION_RELEASED.for_one()))
    }

    fn execute(
        &self,
        _context: &mut Context,
        attendee: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(attendee))
    }
}

#[derive(Default)]
pub struct ConfirmRegistration;

impl Step for ConfirmRegistration {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

static REVIEW_COMPLETED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("reviewCompleted"));

const DESIGN_TIME_REVIEWERS: [&str; 3] = ["legal", "finance", "security"];

/// WCP-13 Multiple Instances with a priori Design-Time Knowledge: the
/// instance count is fixed in the Flow code.
#[derive(Default)]
pub struct DesignTimeMultipleInstancesFlow {
    start_design_time_reviews: StartDesignTimeReviews,
    review_document: ReviewDocument,
    collect_reviews: CollectReviews,
}

impl Flow for DesignTimeMultipleInstancesFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_design_time_reviews)
            .and(&self.review_document)
            .and(&self.collect_reviews)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&REVIEW_COMPLETED)
    }
}

#[derive(Default)]
struct StartDesignTimeReviews;

impl Step for StartDesignTimeReviews {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(start_reviews(
            DESIGN_TIME_REVIEWERS.map(str::to_string).to_vec(),
        ))
    }
}

/// WCP-14 Multiple Instances with a priori Run-Time Knowledge: the instance
/// count comes from the Flow input.
#[derive(Default)]
pub struct RunTimeMultipleInstancesFlow {
    start_run_time_reviews: StartRunTimeReviews,
    review_document: ReviewDocument,
    collect_reviews: CollectReviews,
}

impl Flow for RunTimeMultipleInstancesFlow {
    type StartInput = Vec<String>;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_run_time_reviews)
            .and(&self.review_document)
            .and(&self.collect_reviews)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&REVIEW_COMPLETED)
    }
}

#[derive(Default)]
struct StartRunTimeReviews;

impl Step for StartRunTimeReviews {
    type Input = Vec<String>;

    fn execute(
        &self,
        _context: &mut Context,
        reviewers: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(start_reviews(reviewers))
    }
}

/// Starts one ReviewDocument per reviewer and the join that waits for all.
fn start_reviews(reviewers: Vec<String>) -> StepDecision {
    let collect_reviews = StepMovement::to(&CollectReviews, reviewers.len());
    let reviews = reviewers
        .into_iter()
        .map(|reviewer| StepMovement::to(&ReviewDocument, reviewer));
    StepDecision::go_to_many(reviews.chain([collect_reviews]))
}

#[derive(Default)]
struct ReviewDocument;

impl Step for ReviewDocument {
    type Input = String;

    fn execute(&self, context: &mut Context, reviewer: Self::Input) -> HandlerResult<StepDecision> {
        REVIEW_COMPLETED.publish(context, reviewer)?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct CollectReviews;

impl Step for CollectReviews {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, review_count: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(REVIEW_COMPLETED.for_n(review_count)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _review_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let reviewers = REVIEW_COMPLETED.condition_results(context)?;
        Ok(StepDecision::graceful_complete(sorted(reviewers)))
    }
}

pub static STARTED_SAMPLE_COUNT: LazyLock<Attribute<usize>> =
    LazyLock::new(|| Attribute::new("startedSampleCount"));
pub static IS_SAMPLE_INTAKE_CLOSED: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("isSampleIntakeClosed"));
static SAMPLE_ANALYZED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("sampleAnalyzed"));
static SAMPLE_INTAKE_CLOSED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("sampleIntakeClosed"));

pub const ADD_SAMPLE: Rpc<String, bool> = Rpc::new("add_sample");
pub const CLOSE_SAMPLE_INTAKE: Rpc<(), ()> = Rpc::new("close_sample_intake");

/// WCP-15 Multiple Instances without a priori Run-Time Knowledge: RPCs add
/// instances while the join runs, and the join loops until intake closes
/// and every started instance has reported.
#[derive(Default)]
pub struct NoPriorKnowledgeMultipleInstancesFlow {
    join_samples: JoinSamples,
    analyze_sample: AnalyzeSample,
    report_samples: ReportSamples,
}

impl NoPriorKnowledgeMultipleInstancesFlow {
    fn add_sample(
        &self,
        context: &mut Context,
        sample_id: String,
    ) -> HandlerResult<RpcResult<bool>> {
        if IS_SAMPLE_INTAKE_CLOSED.get(context)?.unwrap_or(false) {
            return Ok(RpcResult::new(false));
        }
        let started_sample_count = STARTED_SAMPLE_COUNT.get(context)?.unwrap_or(0);
        STARTED_SAMPLE_COUNT.set(context, started_sample_count + 1)?;
        Ok(RpcResult::new(true).then(StepMovement::to(&AnalyzeSample, sample_id)))
    }

    fn close_sample_intake(&self, context: &mut Context) -> HandlerResult<()> {
        IS_SAMPLE_INTAKE_CLOSED.set(context, true)?;
        SAMPLE_INTAKE_CLOSED.publish(context, "closed".to_string())
    }
}

impl Flow for NoPriorKnowledgeMultipleInstancesFlow {
    type StartInput = Vec<String>;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.join_samples)
            .and(&self.analyze_sample)
            .and(&self.report_samples)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&STARTED_SAMPLE_COUNT)
            .attribute(&IS_SAMPLE_INTAKE_CLOSED)
            .channel(&SAMPLE_ANALYZED)
            .channel(&SAMPLE_INTAKE_CLOSED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(
                ADD_SAMPLE.lock(STARTED_SAMPLE_COUNT.lock()),
                Self::add_sample,
            )
            .procedure_without_input(
                CLOSE_SAMPLE_INTAKE.lock(STARTED_SAMPLE_COUNT.lock()),
                Self::close_sample_intake,
            )
    }
}

#[derive(Default)]
struct JoinSamples;

impl Step for JoinSamples {
    type Input = Vec<String>;

    fn wait_for(
        &self,
        context: &mut Context,
        analyzed_samples: Self::Input,
    ) -> HandlerResult<Wait> {
        let is_intake_closed = IS_SAMPLE_INTAKE_CLOSED.get(context)?.unwrap_or(false);
        if !is_intake_closed {
            return Ok(Wait::any_of([
                SAMPLE_ANALYZED.for_one(),
                SAMPLE_INTAKE_CLOSED.for_one(),
            ]));
        }
        let started_sample_count = STARTED_SAMPLE_COUNT.get(context)?.unwrap_or(0);
        if analyzed_samples.len() == started_sample_count {
            return Ok(Wait::skip_immediately());
        }
        Ok(Wait::until(SAMPLE_ANALYZED.for_one()))
    }

    fn execute(
        &self,
        context: &mut Context,
        mut analyzed_samples: Self::Input,
    ) -> HandlerResult<StepDecision> {
        analyzed_samples.extend(SAMPLE_ANALYZED.condition_results(context)?);
        let is_intake_closed = IS_SAMPLE_INTAKE_CLOSED.get(context)?.unwrap_or(false);
        let started_sample_count = STARTED_SAMPLE_COUNT.get(context)?.unwrap_or(0);
        if is_intake_closed && analyzed_samples.len() == started_sample_count {
            return Ok(StepDecision::go_to(&ReportSamples, analyzed_samples));
        }
        Ok(StepDecision::go_to(&JoinSamples, analyzed_samples))
    }
}

#[derive(Default)]
struct AnalyzeSample;

impl Step for AnalyzeSample {
    type Input = String;

    fn execute(
        &self,
        context: &mut Context,
        sample_id: Self::Input,
    ) -> HandlerResult<StepDecision> {
        SAMPLE_ANALYZED.publish(context, sample_id)?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct ReportSamples;

impl Step for ReportSamples {
    type Input = Vec<String>;

    fn execute(
        &self,
        _context: &mut Context,
        analyzed_samples: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(sorted(analyzed_samples)))
    }
}

/// How many inspection instances to start, and how many complete the join.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InspectionPlan {
    pub inspection_count: usize,
    pub required_inspections: usize,
}

/// The inspections the join waits for, and those its reset absorbs.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InspectionJoin {
    pub required_inspections: usize,
    pub remaining_inspections: usize,
}

static INSPECTION_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("inspectionReleased"));
static UNIT_INSPECTED: LazyLock<Channel<usize>> = LazyLock::new(|| Channel::new("unitInspected"));

pub const RELEASE_INSPECTIONS: Rpc<usize, ()> = Rpc::new("release_inspections");

/// WCP-34 Static Partial Join for Multiple Instances: the join fires after
/// the required instances, and a reset Step absorbs the rest.
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
    type StartInput = InspectionPlan;

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
            .channel(&UNIT_INSPECTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(RELEASE_INSPECTIONS, Self::release_inspections)
    }
}

#[derive(Default)]
struct StartInspections;

impl Step for StartInspections {
    type Input = InspectionPlan;

    fn execute(&self, _context: &mut Context, plan: Self::Input) -> HandlerResult<StepDecision> {
        if plan.required_inspections > plan.inspection_count {
            return Ok(StepDecision::force_fail(
                "required inspections exceed the inspection count",
            ));
        }
        let join = InspectionJoin {
            required_inspections: plan.required_inspections,
            remaining_inspections: plan.inspection_count - plan.required_inspections,
        };
        let inspections =
            (0..plan.inspection_count).map(|index| StepMovement::to(&InspectUnit, index));
        Ok(StepDecision::go_to_many(
            inspections.chain([StepMovement::to(&AcceptInspections, join)]),
        ))
    }
}

#[derive(Default)]
struct InspectUnit;

impl Step for InspectUnit {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, _index: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(INSPECTION_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, index: Self::Input) -> HandlerResult<StepDecision> {
        UNIT_INSPECTED.publish(context, index)?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct AcceptInspections;

impl Step for AcceptInspections {
    type Input = InspectionJoin;

    fn wait_for(&self, _context: &mut Context, join: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(UNIT_INSPECTED.for_n(join.required_inspections)))
    }

    fn execute(&self, context: &mut Context, join: Self::Input) -> HandlerResult<StepDecision> {
        let inspected_units = UNIT_INSPECTED.condition_results(context)?;
        Ok(StepDecision::go_to_many([
            StepMovement::to(&ApproveBatch, inspected_units),
            StepMovement::to(&ResetInspectionJoin, join.remaining_inspections),
        ]))
    }
}

#[derive(Default)]
pub struct ApproveBatch;

impl Step for ApproveBatch {
    type Input = Vec<usize>;

    fn execute(
        &self,
        _context: &mut Context,
        inspected_units: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(sorted(inspected_units)))
    }
}

#[derive(Default)]
struct ResetInspectionJoin;

impl Step for ResetInspectionJoin {
    type Input = usize;

    fn wait_for(
        &self,
        _context: &mut Context,
        remaining_inspections: Self::Input,
    ) -> HandlerResult<Wait> {
        Ok(Wait::until(UNIT_INSPECTED.for_n(remaining_inspections)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _remaining_inspections: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let inspected_units = UNIT_INSPECTED.condition_results(context)?;
        Ok(StepDecision::graceful_complete(sorted(inspected_units)))
    }
}

static QUORUM_INSPECTION_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("quorumInspectionReleased"));
static QUORUM_UNIT_INSPECTED: LazyLock<Channel<usize>> =
    LazyLock::new(|| Channel::new("quorumUnitInspected"));

pub const RELEASE_QUORUM_INSPECTIONS: Rpc<usize, ()> = Rpc::new("release_quorum_inspections");

/// WCP-35 Cancelling Partial Join for Multiple Instances: the join cancels
/// the instances that have not reported.
#[derive(Default)]
pub struct CancellingPartialJoinForMultipleInstancesFlow {
    start_quorum_inspections: StartQuorumInspections,
    inspect_quorum_unit: InspectQuorumUnit,
    accept_quorum_inspections: AcceptQuorumInspections,
}

impl CancellingPartialJoinForMultipleInstancesFlow {
    fn release_quorum_inspections(&self, context: &mut Context, count: usize) -> HandlerResult<()> {
        for _ in 0..count {
            QUORUM_INSPECTION_RELEASED.publish(context, "released".to_string())?;
        }
        Ok(())
    }
}

impl Flow for CancellingPartialJoinForMultipleInstancesFlow {
    type StartInput = InspectionPlan;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_quorum_inspections)
            .and(&self.inspect_quorum_unit)
            .and(&self.accept_quorum_inspections)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&QUORUM_INSPECTION_RELEASED)
            .channel(&QUORUM_UNIT_INSPECTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(RELEASE_QUORUM_INSPECTIONS, Self::release_quorum_inspections)
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
struct AcceptQuorumInspections;

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
        Ok(
            StepDecision::graceful_complete(sorted(inspected_units))
                .cancel_step(&InspectQuorumUnit),
        )
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
static BID_EVALUATED: LazyLock<Channel<BidEvaluation>> =
    LazyLock::new(|| Channel::new("bidEvaluated"));

pub const ADD_BID: Rpc<i64, bool> = Rpc::new("add_bid");

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
            .channel(&BID_EVALUATED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(ADD_BID.lock(IS_BID_ADMISSION_CLOSED.lock()), Self::add_bid)
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

    fn execute(
        &self,
        _context: &mut Context,
        accepted_bids: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(sorted(accepted_bids)))
    }
}
