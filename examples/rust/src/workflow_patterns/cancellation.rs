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

//! Cancellation: Cancel Task (WCP-19), Cancel Case (WCP-20), Cancel Region
//! (WCP-25), Cancel Multiple Instance Activity (WCP-26), and Complete
//! Multiple Instance Activity (WCP-27).

use std::sync::{Arc, LazyLock};

use dex_sdk::{
    Attribute, Channel, Client, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc,
    RpcList, RpcResult, SdkError, Step, StepDecision, StepList, StepMovement, StepOptions,
    StopFlowOptions, SubFlow, Wait,
};

use super::sorted;

static COURIER_ARRIVED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("courierArrived"));

pub const CONFIRM_COURIER_PICKUP: Rpc<(), ()> = Rpc::new("confirm_courier_pickup");
pub const CANCEL_COURIER_PICKUP: Rpc<(), ()> = Rpc::new("cancel_courier_pickup");

/// WCP-19 Cancel Task: an RPC cancels the waiting pickup Step and starts
/// the fallback Step.
#[derive(Default)]
pub struct CancelTaskFlow {
    await_courier_pickup: AwaitCourierPickup,
    arrange_self_delivery: ArrangeSelfDelivery,
}

impl CancelTaskFlow {
    fn confirm_courier_pickup(&self, context: &mut Context) -> HandlerResult<()> {
        COURIER_ARRIVED.publish(context, "arrived".to_string())
    }

    fn cancel_courier_pickup(&self, _context: &mut Context) -> HandlerResult<RpcResult<()>> {
        Ok(RpcResult::new(())
            .then(StepMovement::to(&ArrangeSelfDelivery, ()))
            .cancel_step(&AwaitCourierPickup))
    }
}

impl Flow for CancelTaskFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.await_courier_pickup).and(&self.arrange_self_delivery)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&COURIER_ARRIVED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(CONFIRM_COURIER_PICKUP, Self::confirm_courier_pickup)
            .function_without_input(CANCEL_COURIER_PICKUP, Self::cancel_courier_pickup)
    }
}

#[derive(Default)]
struct AwaitCourierPickup;

impl Step for AwaitCourierPickup {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(COURIER_ARRIVED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct ArrangeSelfDelivery;

impl Step for ArrangeSelfDelivery {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

pub static STOPPED_CHILD_FLOW_IDS: LazyLock<Attribute<Vec<String>>> =
    LazyLock::new(|| Attribute::new("stoppedChildFlowIds"));
static APPLICATION_REVIEWED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("applicationReviewed"));
static CANCEL_CASE_REQUESTED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("cancelCase"));

pub const CANCEL_CASE: Rpc<String, ()> = Rpc::new("cancel_case");

/// WCP-20 Cancel Case: the Step that starts the child Flow also waits for a
/// cancel request. On cancel it stops the child, then fails the case, which
/// cancels every other active Step.
#[derive(Default)]
pub struct CancelCaseFlow {
    start_application: StartApplication,
    review_application: ReviewApplication,
    run_background_check: RunBackgroundCheck,
    watch_case_cancellation: WatchCaseCancellation,
}

impl CancelCaseFlow {
    /// Creates the Flow for a Worker, whose background check stops its child
    /// Flow through `client`.
    pub fn new(client: Arc<Client>) -> Self {
        Self {
            run_background_check: RunBackgroundCheck {
                client: Some(client),
            },
            ..Self::default()
        }
    }

    fn cancel_case(&self, context: &mut Context, reason: String) -> HandlerResult<()> {
        CANCEL_CASE_REQUESTED.publish(context, reason)
    }
}

impl Flow for CancelCaseFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_application)
            .and(&self.review_application)
            .and(&self.run_background_check)
            .and(&self.watch_case_cancellation)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&STOPPED_CHILD_FLOW_IDS)
            .channel(&APPLICATION_REVIEWED)
            .channel(&CANCEL_CASE_REQUESTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(CANCEL_CASE, Self::cancel_case)
    }
}

#[derive(Default)]
pub struct StartApplication;

impl Step for StartApplication {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&ReviewApplication, ()),
            StepMovement::to(&RunBackgroundCheck::default(), ()),
        ]))
    }
}

#[derive(Default)]
struct ReviewApplication;

impl Step for ReviewApplication {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(APPLICATION_REVIEWED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct RunBackgroundCheck {
    client: Option<Arc<Client>>,
}

impl RunBackgroundCheck {
    /// Stops the child Flow; a missing or closed child already stopped.
    fn stop_child_flow(&self, child_flow_id: &str) -> HandlerResult<()> {
        let client = self
            .client
            .as_ref()
            .ok_or_else(|| HandlerError::new("CancelCase", "the Dex Client is not initialized"))?;
        match client.stop_flow(
            child_flow_id,
            StopFlowOptions::cancel().reason("parent case canceled"),
        ) {
            Ok(())
            | Err(
                SdkError::FlowNotFound { .. }
                | SdkError::FlowNotActiveOrNotFound { .. }
                | SdkError::FlowClosing { .. },
            ) => Ok(()),
            Err(error) => Err(HandlerError::from_error(error)),
        }
    }
}

impl Step for RunBackgroundCheck {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        let background_check =
            SubFlow::run(&BackgroundCheckFlow::default(), ()).map_err(HandlerError::from_error)?;
        Ok(Wait::any_of([
            background_check,
            CANCEL_CASE_REQUESTED.for_one(),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let Some(reason) = CANCEL_CASE_REQUESTED
            .condition_results(context)?
            .into_iter()
            .next()
        else {
            return Ok(StepDecision::go_to(&WatchCaseCancellation, ()));
        };
        let child_flow_id = SubFlow::flow_id_at(context, 0)?;
        self.stop_child_flow(&child_flow_id)?;
        STOPPED_CHILD_FLOW_IDS.set(context, vec![child_flow_id])?;
        Ok(StepDecision::force_fail(format!("case canceled: {reason}")))
    }
}

/// Covers a cancel request that arrives after the child Flow finished.
#[derive(Default)]
struct WatchCaseCancellation;

impl Step for WatchCaseCancellation {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CANCEL_CASE_REQUESTED.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let reason = CANCEL_CASE_REQUESTED
            .condition_results(context)?
            .into_iter()
            .next()
            .ok_or_else(|| {
                HandlerError::new("MissingCancelReason", "no cancel request consumed")
            })?;
        Ok(StepDecision::force_fail(format!("case canceled: {reason}")))
    }
}

static BACKGROUND_CHECK_COMPLETED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("backgroundCheckCompleted"));

/// The child Flow of WCP-20. It parks until a check result arrives.
#[derive(Default)]
pub struct BackgroundCheckFlow {
    await_background_check: AwaitBackgroundCheck,
}

impl Flow for BackgroundCheckFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.await_background_check)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&BACKGROUND_CHECK_COMPLETED)
    }
}

#[derive(Default)]
struct AwaitBackgroundCheck;

impl Step for AwaitBackgroundCheck {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(BACKGROUND_CHECK_COMPLETED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

static IDENTITY_VERIFIED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("identityVerified"));
static CREDIT_HISTORY_CHECKED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("creditHistoryChecked"));
static SANCTIONS_SCREENED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("sanctionsScreened"));

pub const REJECT_APPLICATION: Rpc<(), ()> = Rpc::new("reject_application");
pub const COMPLETE_SANCTIONS_SCREENING: Rpc<(), ()> = Rpc::new("complete_sanctions_screening");

/// WCP-25 Cancel Region: one RPC cancels two of the three checks and
/// leaves the sanctions screening running.
#[derive(Default)]
pub struct CancelRegionFlow {
    start_application_checks: StartApplicationChecks,
    verify_identity: VerifyIdentity,
    check_credit_history: CheckCreditHistory,
    screen_sanctions: ScreenSanctions,
    notify_rejection: NotifyRejection,
}

impl CancelRegionFlow {
    fn reject_application(&self, _context: &mut Context) -> HandlerResult<RpcResult<()>> {
        Ok(RpcResult::new(())
            .then(StepMovement::to(&NotifyRejection, ()))
            .cancel_step(&VerifyIdentity)
            .cancel_step(&CheckCreditHistory))
    }

    fn complete_sanctions_screening(&self, context: &mut Context) -> HandlerResult<()> {
        SANCTIONS_SCREENED.publish(context, "screened".to_string())
    }
}

impl Flow for CancelRegionFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_application_checks)
            .and(&self.verify_identity)
            .and(&self.check_credit_history)
            .and(&self.screen_sanctions)
            .and(&self.notify_rejection)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&IDENTITY_VERIFIED)
            .channel(&CREDIT_HISTORY_CHECKED)
            .channel(&SANCTIONS_SCREENED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(REJECT_APPLICATION, Self::reject_application)
            .procedure_without_input(
                COMPLETE_SANCTIONS_SCREENING,
                Self::complete_sanctions_screening,
            )
    }
}

#[derive(Default)]
pub struct StartApplicationChecks;

impl Step for StartApplicationChecks {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&VerifyIdentity, ()),
            StepMovement::to(&CheckCreditHistory, ()),
            StepMovement::to(&ScreenSanctions, ()),
        ]))
    }
}

#[derive(Default)]
struct VerifyIdentity;

impl Step for VerifyIdentity {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(IDENTITY_VERIFIED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct CheckCreditHistory;

impl Step for CheckCreditHistory {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CREDIT_HISTORY_CHECKED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct ScreenSanctions;

impl Step for ScreenSanctions {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(SANCTIONS_SCREENED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
pub struct NotifyRejection;

impl Step for NotifyRejection {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

pub static FINISHED_TRANSLATION_COUNT: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("finishedTranslationCount"));
static TRANSLATION_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("translationReleased"));

pub const RELEASE_TRANSLATION: Rpc<(), ()> = Rpc::new("release_translation");
pub const CANCEL_TRANSLATIONS: Rpc<(), ()> = Rpc::new("cancel_translations");

/// WCP-26 Cancel Multiple Instance Activity: an RPC cancels every running
/// translation instance, and finished instances keep their results.
#[derive(Default)]
pub struct CancelMultipleInstanceTaskFlow {
    start_translations: StartTranslations,
    translate_document: TranslateDocument,
    report_translations: ReportTranslations,
}

impl CancelMultipleInstanceTaskFlow {
    fn release_translation(&self, context: &mut Context) -> HandlerResult<()> {
        TRANSLATION_RELEASED.publish(context, "released".to_string())
    }

    fn cancel_translations(&self, _context: &mut Context) -> HandlerResult<RpcResult<()>> {
        Ok(RpcResult::new(())
            .then(StepMovement::to(&ReportTranslations, ()))
            .cancel_step(&TranslateDocument))
    }
}

impl Flow for CancelMultipleInstanceTaskFlow {
    type StartInput = usize;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_translations)
            .and(&self.translate_document)
            .and(&self.report_translations)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&FINISHED_TRANSLATION_COUNT)
            .channel(&TRANSLATION_RELEASED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(RELEASE_TRANSLATION, Self::release_translation)
            .function_without_input(CANCEL_TRANSLATIONS, Self::cancel_translations)
    }
}

#[derive(Default)]
pub struct StartTranslations;

impl Step for StartTranslations {
    type Input = usize;

    fn execute(
        &self,
        _context: &mut Context,
        translation_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many(
            (0..translation_count).map(|index| StepMovement::to(&TranslateDocument, index)),
        ))
    }
}

#[derive(Default)]
struct TranslateDocument;

impl Step for TranslateDocument {
    type Input = usize;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(FINISHED_TRANSLATION_COUNT.lock())
    }

    fn wait_for(&self, _context: &mut Context, _index: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(TRANSLATION_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, index: Self::Input) -> HandlerResult<StepDecision> {
        let finished_translation_count = FINISHED_TRANSLATION_COUNT.get(context)?.unwrap_or(0);
        FINISHED_TRANSLATION_COUNT.set(context, finished_translation_count + 1)?;
        Ok(StepDecision::graceful_complete(index))
    }
}

#[derive(Default)]
struct ReportTranslations;

impl Step for ReportTranslations {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let finished_translation_count = FINISHED_TRANSLATION_COUNT.get(context)?.unwrap_or(0);
        Ok(StepDecision::graceful_complete(finished_translation_count))
    }
}

static PEER_REVIEW_RELEASED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("peerReviewReleased"));
static PEER_REVIEW_SUBMITTED: LazyLock<Channel<usize>> =
    LazyLock::new(|| Channel::new("peerReviewSubmitted"));

pub const RELEASE_PEER_REVIEW: Rpc<(), ()> = Rpc::new("release_peer_review");
pub const GET_SUBMITTED_PEER_REVIEW_COUNT: Rpc<(), usize> =
    Rpc::new("get_submitted_peer_review_count");
pub const COMPLETE_PEER_REVIEWS_EARLY: Rpc<(), ()> = Rpc::new("complete_peer_reviews_early");

/// WCP-27 Complete Multiple Instance Activity: an RPC cancels the remaining
/// instances and the join, and continues with the reviews submitted so far.
#[derive(Default)]
pub struct CompleteMultipleInstanceTaskFlow {
    start_peer_reviews: StartPeerReviews,
    submit_peer_review: SubmitPeerReview,
    collect_peer_reviews: CollectPeerReviews,
    publish_review_results: PublishReviewResults,
}

impl CompleteMultipleInstanceTaskFlow {
    fn release_peer_review(&self, context: &mut Context) -> HandlerResult<()> {
        PEER_REVIEW_RELEASED.publish(context, "released".to_string())
    }

    fn get_submitted_peer_review_count(
        &self,
        context: &mut Context,
    ) -> HandlerResult<RpcResult<usize>> {
        let submitted_reviews = PEER_REVIEW_SUBMITTED.pending_messages(context)?;
        Ok(RpcResult::new(submitted_reviews.len()))
    }

    fn complete_peer_reviews_early(&self, context: &mut Context) -> HandlerResult<RpcResult<()>> {
        let submitted_indexes = PEER_REVIEW_SUBMITTED
            .pending_messages(context)?
            .into_iter()
            .map(|message| message.value)
            .collect();
        Ok(RpcResult::new(())
            .then(StepMovement::to(
                &PublishReviewResults,
                sorted(submitted_indexes),
            ))
            .cancel_step(&SubmitPeerReview)
            .cancel_step(&CollectPeerReviews))
    }
}

impl Flow for CompleteMultipleInstanceTaskFlow {
    type StartInput = usize;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_peer_reviews)
            .and(&self.submit_peer_review)
            .and(&self.collect_peer_reviews)
            .and(&self.publish_review_results)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&PEER_REVIEW_RELEASED)
            .channel(&PEER_REVIEW_SUBMITTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(RELEASE_PEER_REVIEW, Self::release_peer_review)
            .function_without_input(
                GET_SUBMITTED_PEER_REVIEW_COUNT.load_channel(&PEER_REVIEW_SUBMITTED),
                Self::get_submitted_peer_review_count,
            )
            .function_without_input(
                COMPLETE_PEER_REVIEWS_EARLY.load_channel(&PEER_REVIEW_SUBMITTED),
                Self::complete_peer_reviews_early,
            )
    }
}

#[derive(Default)]
pub struct StartPeerReviews;

impl Step for StartPeerReviews {
    type Input = usize;

    fn execute(
        &self,
        _context: &mut Context,
        reviewer_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let reviews = (0..reviewer_count).map(|index| StepMovement::to(&SubmitPeerReview, index));
        Ok(StepDecision::go_to_many(reviews.chain([StepMovement::to(
            &CollectPeerReviews,
            reviewer_count,
        )])))
    }
}

#[derive(Default)]
struct SubmitPeerReview;

impl Step for SubmitPeerReview {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, _index: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(PEER_REVIEW_RELEASED.for_one()))
    }

    fn execute(&self, context: &mut Context, index: Self::Input) -> HandlerResult<StepDecision> {
        PEER_REVIEW_SUBMITTED.publish(context, index)?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct CollectPeerReviews;

impl Step for CollectPeerReviews {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, reviewer_count: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(PEER_REVIEW_SUBMITTED.for_n(reviewer_count)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _reviewer_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let submitted_indexes = PEER_REVIEW_SUBMITTED.condition_results(context)?;
        Ok(StepDecision::go_to(
            &PublishReviewResults,
            sorted(submitted_indexes),
        ))
    }
}

#[derive(Default)]
struct PublishReviewResults;

impl Step for PublishReviewResults {
    type Input = Vec<usize>;

    fn execute(
        &self,
        _context: &mut Context,
        submitted_indexes: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(submitted_indexes))
    }
}
