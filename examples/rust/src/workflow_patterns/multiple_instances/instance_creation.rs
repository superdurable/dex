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

//! Multiple instances without and with a priori knowledge: WCP-12 to WCP-15.

use std::sync::LazyLock;

use crate::workflow_patterns::sorted;
use dex_sdk::{
    Attribute, AttributeMap, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc,
    RpcList, RpcResult, Step, StepDecision, StepList, StepMovement, Wait,
};

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

pub static STARTED_REVIEWERS: LazyLock<AttributeMap<bool>> =
    LazyLock::new(|| AttributeMap::new("startedReviewers"));
static REVIEW_GATE_OPENED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("reviewGateOpened"));
static REVIEW_COMPLETED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("reviewCompleted"));

pub const GET_STARTED_REVIEW_COUNT: Rpc<(), usize> = Rpc::new("get_started_review_count");
pub const OPEN_REVIEW_GATE: Rpc<usize, ()> = Rpc::new("open_review_gate");

const DESIGN_TIME_REVIEWERS: [&str; 3] = ["legal", "finance", "security"];

/// WCP-13 Multiple Instances with a priori Design-Time Knowledge: the
/// instance count is fixed in the Flow code.
#[derive(Default)]
pub struct DesignTimeMultipleInstancesFlow {
    start_design_time_reviews: StartDesignTimeReviews,
    review_document: ReviewDocument,
    collect_reviews: CollectReviews,
}

impl DesignTimeMultipleInstancesFlow {
    fn get_started_review_count(&self, context: &mut Context) -> HandlerResult<RpcResult<usize>> {
        count_started_reviews(context)
    }

    fn open_review_gate(&self, context: &mut Context, count: usize) -> HandlerResult<()> {
        open_review_gate(context, count)
    }
}

impl Flow for DesignTimeMultipleInstancesFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_design_time_reviews)
            .and(&self.review_document)
            .and(&self.collect_reviews)
    }

    fn persistence(&self) -> PersistenceSchema {
        review_persistence()
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(
                GET_STARTED_REVIEW_COUNT.load_attribute_map(&STARTED_REVIEWERS),
                Self::get_started_review_count,
            )
            .procedure(OPEN_REVIEW_GATE, Self::open_review_gate)
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

impl RunTimeMultipleInstancesFlow {
    fn get_started_review_count(&self, context: &mut Context) -> HandlerResult<RpcResult<usize>> {
        count_started_reviews(context)
    }

    fn open_review_gate(&self, context: &mut Context, count: usize) -> HandlerResult<()> {
        open_review_gate(context, count)
    }
}

impl Flow for RunTimeMultipleInstancesFlow {
    type StartInput = Vec<String>;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_run_time_reviews)
            .and(&self.review_document)
            .and(&self.collect_reviews)
    }

    fn persistence(&self) -> PersistenceSchema {
        review_persistence()
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(
                GET_STARTED_REVIEW_COUNT.load_attribute_map(&STARTED_REVIEWERS),
                Self::get_started_review_count,
            )
            .procedure(OPEN_REVIEW_GATE, Self::open_review_gate)
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

fn review_persistence() -> PersistenceSchema {
    PersistenceSchema::new()
        .attribute_map(&STARTED_REVIEWERS)
        .channel(&REVIEW_GATE_OPENED)
        .channel(&REVIEW_COMPLETED)
}

fn count_started_reviews(context: &Context) -> HandlerResult<RpcResult<usize>> {
    Ok(RpcResult::new(STARTED_REVIEWERS.map_size(context)?))
}

fn open_review_gate(context: &mut Context, count: usize) -> HandlerResult<()> {
    for _ in 0..count {
        REVIEW_GATE_OPENED.publish(context, "opened".to_string())?;
    }
    Ok(())
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

    /// Marks the instance started, then holds it at the concurrency gate.
    fn wait_for(&self, context: &mut Context, reviewer: Self::Input) -> HandlerResult<Wait> {
        STARTED_REVIEWERS.set(context, &reviewer, true)?;
        Ok(Wait::until(REVIEW_GATE_OPENED.for_one()))
    }

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
pub static ANALYSIS_STARTED_SAMPLES: LazyLock<AttributeMap<bool>> =
    LazyLock::new(|| AttributeMap::new("analysisStartedSamples"));
static SAMPLE_ANALYZED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("sampleAnalyzed"));
static SAMPLE_INTAKE_CLOSED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("sampleIntakeClosed"));
static SAMPLE_GATE_OPENED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("sampleGateOpened"));

pub const ADD_SAMPLE: Rpc<String, bool> = Rpc::new("add_sample");
pub const CLOSE_SAMPLE_INTAKE: Rpc<(), ()> = Rpc::new("close_sample_intake");
pub const GET_STARTED_ANALYSIS_COUNT: Rpc<(), usize> = Rpc::new("get_started_analysis_count");
pub const OPEN_SAMPLE_GATE: Rpc<usize, ()> = Rpc::new("open_sample_gate");

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

    fn get_started_analysis_count(&self, context: &mut Context) -> HandlerResult<RpcResult<usize>> {
        Ok(RpcResult::new(ANALYSIS_STARTED_SAMPLES.map_size(context)?))
    }

    fn open_sample_gate(&self, context: &mut Context, count: usize) -> HandlerResult<()> {
        for _ in 0..count {
            SAMPLE_GATE_OPENED.publish(context, "opened".to_string())?;
        }
        Ok(())
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
            .attribute_map(&ANALYSIS_STARTED_SAMPLES)
            .channel(&SAMPLE_ANALYZED)
            .channel(&SAMPLE_INTAKE_CLOSED)
            .channel(&SAMPLE_GATE_OPENED)
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
            .function_without_input(
                GET_STARTED_ANALYSIS_COUNT.load_attribute_map(&ANALYSIS_STARTED_SAMPLES),
                Self::get_started_analysis_count,
            )
            .procedure(OPEN_SAMPLE_GATE, Self::open_sample_gate)
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

    /// Marks the instance started, then holds it at the concurrency gate.
    fn wait_for(&self, context: &mut Context, sample_id: Self::Input) -> HandlerResult<Wait> {
        ANALYSIS_STARTED_SAMPLES.set(context, &sample_id, true)?;
        Ok(Wait::until(SAMPLE_GATE_OPENED.for_one()))
    }

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
