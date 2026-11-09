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

//! Multi-choice and merges: Multi-Choice (WCP-6), Structured Synchronizing
//! Merge (WCP-7), Multi-Merge (WCP-8), Local Synchronizing Merge (WCP-37),
//! and General Synchronizing Merge (WCP-38).

use std::sync::LazyLock;

use dex_sdk::{
    Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Step, StepDecision,
    StepList, StepMovement, SubFlow, Wait,
};
use serde::{Deserialize, Serialize};

use super::sorted;

/// The notification branches a caller selects.
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NotificationChannelSelection {
    pub send_email: bool,
    pub send_sms: bool,
    pub send_push: bool,
}

static NOTIFICATION_SENT: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("notificationSent"));

/// WCP-6 Multi-Choice, WCP-7 Structured Synchronizing Merge, and WCP-37
/// Local Synchronizing Merge: the diverging decision passes the selected
/// branch count to the join Step.
#[derive(Default)]
pub struct MultiChoiceSynchronizingMergeFlow {
    select_notification_channels: SelectNotificationChannels,
    send_email: SendEmail,
    send_sms: SendSms,
    send_push: SendPush,
    synchronizing_merge: SynchronizingMerge,
}

impl Flow for MultiChoiceSynchronizingMergeFlow {
    type StartInput = NotificationChannelSelection;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.select_notification_channels)
            .and(&self.send_email)
            .and(&self.send_sms)
            .and(&self.send_push)
            .and(&self.synchronizing_merge)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&NOTIFICATION_SENT)
    }
}

#[derive(Default)]
struct SelectNotificationChannels;

impl Step for SelectNotificationChannels {
    type Input = NotificationChannelSelection;

    fn execute(
        &self,
        _context: &mut Context,
        selection: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let mut branches = Vec::new();
        if selection.send_email {
            branches.push(StepMovement::to(&SendEmail, ()));
        }
        if selection.send_sms {
            branches.push(StepMovement::to(&SendSms, ()));
        }
        if selection.send_push {
            branches.push(StepMovement::to(&SendPush, ()));
        }
        if branches.is_empty() {
            return Ok(StepDecision::graceful_complete(Vec::<String>::new()));
        }
        let merge = StepMovement::to(&SynchronizingMerge, branches.len());
        Ok(StepDecision::go_to_many(
            std::iter::once(merge).chain(branches),
        ))
    }
}

#[derive(Default)]
struct SendEmail;

impl Step for SendEmail {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        NOTIFICATION_SENT.publish(context, self.step_type().to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct SendSms;

impl Step for SendSms {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        NOTIFICATION_SENT.publish(context, self.step_type().to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct SendPush;

impl Step for SendPush {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        NOTIFICATION_SENT.publish(context, self.step_type().to_string())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct SynchronizingMerge;

impl Step for SynchronizingMerge {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, branch_count: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(NOTIFICATION_SENT.for_n(branch_count)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _branch_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let notifications = NOTIFICATION_SENT.condition_results(context)?;
        Ok(StepDecision::graceful_complete(sorted(notifications)))
    }
}

/// WCP-8 Multi-Merge: each arriving branch starts a new execution of the
/// same Step type.
#[derive(Default)]
pub struct MultiMergeFlow {
    start_reviews: StartReviews,
    legal_review: LegalReview,
    finance_review: FinanceReview,
    record_review: RecordReview,
}

impl Flow for MultiMergeFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_reviews)
            .and(&self.legal_review)
            .and(&self.finance_review)
            .and(&self.record_review)
    }
}

#[derive(Default)]
struct StartReviews;

impl Step for StartReviews {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&LegalReview, ()),
            StepMovement::to(&FinanceReview, ()),
        ]))
    }
}

#[derive(Default)]
struct LegalReview;

impl Step for LegalReview {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &RecordReview,
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct FinanceReview;

impl Step for FinanceReview {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &RecordReview,
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct RecordReview;

impl Step for RecordReview {
    type Input = String;

    fn execute(
        &self,
        _context: &mut Context,
        reviewer: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(reviewer))
    }
}

/// WCP-38 General Synchronizing Merge: the region runs as a SubFlow, which
/// completes once no Step in it can run, however often its cycle repeats.
#[derive(Default)]
pub struct GeneralSynchronizingMergeFlow {
    run_review_region: RunReviewRegion,
    publish_decision: PublishDecision,
}

impl Flow for GeneralSynchronizingMergeFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.run_review_region).and(&self.publish_decision)
    }
}

#[derive(Default)]
struct RunReviewRegion;

impl Step for RunReviewRegion {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        let region =
            SubFlow::run(&ReviewRegionFlow::default(), 0).map_err(HandlerError::from_error)?;
        Ok(Wait::until(region))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let region_result = SubFlow::condition_result(context)?;
        let outputs = region_result
            .completions()
            .iter()
            .map(|completion| completion.decode::<String>())
            .collect::<Result<Vec<_>, _>>()
            .map_err(HandlerError::from_error)?;
        Ok(StepDecision::go_to(&PublishDecision, sorted(outputs)))
    }
}

#[derive(Default)]
struct PublishDecision;

impl Step for PublishDecision {
    type Input = Vec<String>;

    fn execute(&self, _context: &mut Context, outputs: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(outputs))
    }
}

/// The SubFlow region of WCP-38: a review cycle that drafts a proposal
/// again until its revision reaches 1, in parallel with translations.
#[derive(Default)]
pub struct ReviewRegionFlow {
    draft_proposal: DraftProposal,
    review_proposal: ReviewProposal,
    translate_proposal: TranslateProposal,
}

impl Flow for ReviewRegionFlow {
    type StartInput = i64;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.draft_proposal)
            .and(&self.review_proposal)
            .and(&self.translate_proposal)
    }
}

const ACCEPTED_PROPOSAL_REVISION: i64 = 1;

#[derive(Default)]
struct DraftProposal;

impl Step for DraftProposal {
    type Input = i64;

    fn execute(
        &self,
        _context: &mut Context,
        revision: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&ReviewProposal, revision),
            StepMovement::to(&TranslateProposal, revision),
        ]))
    }
}

#[derive(Default)]
struct ReviewProposal;

impl Step for ReviewProposal {
    type Input = i64;

    fn execute(
        &self,
        _context: &mut Context,
        revision: Self::Input,
    ) -> HandlerResult<StepDecision> {
        if revision < ACCEPTED_PROPOSAL_REVISION {
            return Ok(StepDecision::go_to(&DraftProposal, revision + 1));
        }
        Ok(StepDecision::graceful_complete(format!(
            "{}-{revision}",
            self.step_type()
        )))
    }
}

#[derive(Default)]
struct TranslateProposal;

impl Step for TranslateProposal {
    type Input = i64;

    fn execute(
        &self,
        _context: &mut Context,
        revision: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!(
            "{}-{revision}",
            self.step_type()
        )))
    }
}
