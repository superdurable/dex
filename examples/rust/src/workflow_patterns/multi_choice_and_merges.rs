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
    Attribute, Channel, Context, Flow, HandlerError, HandlerResult, Join, JoinRound,
    PersistenceSchema, Step, StepDecision, StepList, StepMovement, StepOptions, Value, Wait,
};
use serde::{Deserialize, Serialize};

use super::{round_branch, sorted};

/// The notification branches a caller selects.
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NotificationChannelSelection {
    pub send_email: bool,
    pub send_sms: bool,
    pub send_push: bool,
}

static NOTIFICATION_SENT: LazyLock<Join<String>> = LazyLock::new(|| Join::new("notificationSent"));

/// WCP-6 Multi-Choice, WCP-7 Structured Synchronizing Merge, and WCP-37
/// Local Synchronizing Merge: the diverging decision forks the selected
/// branches, and the join waits for each of them.
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
        PersistenceSchema::new().join(&NOTIFICATION_SENT)
    }
}

#[derive(Default)]
struct SelectNotificationChannels;

impl Step for SelectNotificationChannels {
    type Input = NotificationChannelSelection;

    fn execute(
        &self,
        context: &mut Context,
        selection: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let mut branches = Vec::new();
        if selection.send_email {
            branches.push(round_branch(SendEmail));
        }
        if selection.send_sms {
            branches.push(round_branch(SendSms));
        }
        if selection.send_push {
            branches.push(round_branch(SendPush));
        }
        if branches.is_empty() {
            return Ok(StepDecision::graceful_complete(Vec::<String>::new()));
        }
        let movements = NOTIFICATION_SENT.fork(context, branches, |round| {
            StepMovement::to(&SynchronizingMerge, round)
        })?;
        Ok(StepDecision::go_to_many(movements))
    }
}

#[derive(Default)]
struct SendEmail;

impl Step for SendEmail {
    type Input = JoinRound;

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        NOTIFICATION_SENT.end_branch(context, &round, self.step_type().to_string())
    }
}

#[derive(Default)]
struct SendSms;

impl Step for SendSms {
    type Input = JoinRound;

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        NOTIFICATION_SENT.end_branch(context, &round, self.step_type().to_string())
    }
}

#[derive(Default)]
struct SendPush;

impl Step for SendPush {
    type Input = JoinRound;

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        NOTIFICATION_SENT.end_branch(context, &round, self.step_type().to_string())
    }
}

#[derive(Default)]
struct SynchronizingMerge;

impl Step for SynchronizingMerge {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(NOTIFICATION_SENT.for_all_branches(&round)))
    }

    fn execute(&self, context: &mut Context, round: Self::Input) -> HandlerResult<StepDecision> {
        let notifications = NOTIFICATION_SENT.branch_results(context, &round)?;
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

/// Which checks a document review runs and how its finance branch ends.
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DocumentReviewPlan {
    pub is_legal_check_needed: bool,
    pub is_finance_check_needed: bool,
    pub finance_rework_rounds: u32,
    pub is_finance_archived: bool,
}

/// One draft of the document under review.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DocumentDraft {
    pub plan: DocumentReviewPlan,
    pub revision: u32,
}

static LIVE_BRANCH_COUNT: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("liveBranchCount"));
static MERGED_REVIEWS: LazyLock<Attribute<Vec<String>>> =
    LazyLock::new(|| Attribute::new("mergedReviews"));
static MERGE_SENTINEL: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("mergeSentinel"));

/// WCP-38 General Synchronizing Merge: an Attribute counts the threads that
/// can still reach the merge, and the commit that brings it to 0 publishes
/// the sentinel the merge waits for.
#[derive(Default)]
pub struct GeneralSynchronizingMergeFlow {
    start_document_review: StartDocumentReview,
    draft_document: DraftDocument,
    check_legal_terms: CheckLegalTerms,
    check_finance_terms: CheckFinanceTerms,
    archive_document: ArchiveDocument,
    publish_document: PublishDocument,
}

impl Flow for GeneralSynchronizingMergeFlow {
    type StartInput = DocumentReviewPlan;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_document_review)
            .and(&self.draft_document)
            .and(&self.check_legal_terms)
            .and(&self.check_finance_terms)
            .and(&self.archive_document)
            .and(&self.publish_document)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&LIVE_BRANCH_COUNT)
            .attribute(&MERGED_REVIEWS)
            .channel(&MERGE_SENTINEL)
    }
}

/// Holds the lock on the live branch count for one Execute.
fn live_branch_lock_options<Input: Value>() -> StepOptions<Input> {
    StepOptions::new().execute_lock(LIVE_BRANCH_COUNT.lock())
}

/// Adds `delta` to the live branch count and publishes the sentinel when no
/// thread can reach the merge any more.
fn change_live_branch_count(context: &mut Context, delta: i64) -> HandlerResult<()> {
    let live_branch_count = LIVE_BRANCH_COUNT.get(context)?.unwrap_or(0) + delta;
    LIVE_BRANCH_COUNT.set(context, live_branch_count)?;
    if live_branch_count == 0 {
        MERGE_SENTINEL.publish(context, "done".to_string())?;
    }
    Ok(())
}

/// Records one arrival at the merge and retires its thread.
fn record_merge_arrival(context: &mut Context, review: String) -> HandlerResult<()> {
    let mut merged_reviews = MERGED_REVIEWS.get(context)?.unwrap_or_default();
    merged_reviews.push(review);
    MERGED_REVIEWS.set(context, merged_reviews)?;
    change_live_branch_count(context, -1)
}

#[derive(Default)]
struct StartDocumentReview;

impl Step for StartDocumentReview {
    type Input = DocumentReviewPlan;

    fn options(&self) -> StepOptions<Self::Input> {
        live_branch_lock_options()
    }

    fn execute(&self, context: &mut Context, plan: Self::Input) -> HandlerResult<StepDecision> {
        LIVE_BRANCH_COUNT.set(context, 1)?;
        Ok(StepDecision::go_to_many([
            StepMovement::to(&DraftDocument, DocumentDraft { plan, revision: 0 }),
            StepMovement::to(&PublishDocument, ()),
        ]))
    }
}

#[derive(Default)]
struct DraftDocument;

impl Step for DraftDocument {
    type Input = DocumentDraft;

    fn options(&self) -> StepOptions<Self::Input> {
        live_branch_lock_options()
    }

    fn execute(&self, context: &mut Context, draft: Self::Input) -> HandlerResult<StepDecision> {
        let is_rework = draft.revision > 0;
        let mut checks = Vec::new();
        if is_rework || draft.plan.is_legal_check_needed {
            checks.push(StepMovement::to(&CheckLegalTerms, draft.revision));
        }
        if is_rework || draft.plan.is_finance_check_needed {
            checks.push(StepMovement::to(&CheckFinanceTerms, draft));
        }
        let selected_check_count = i64::try_from(checks.len()).map_err(HandlerError::from_error)?;
        change_live_branch_count(context, selected_check_count - 1)?;
        if checks.is_empty() {
            return Ok(StepDecision::dead_end());
        }
        Ok(StepDecision::go_to_many(checks))
    }
}

#[derive(Default)]
struct CheckLegalTerms;

impl Step for CheckLegalTerms {
    type Input = u32;

    fn options(&self) -> StepOptions<Self::Input> {
        live_branch_lock_options()
    }

    fn execute(&self, context: &mut Context, revision: Self::Input) -> HandlerResult<StepDecision> {
        record_merge_arrival(context, format!("{}-{revision}", self.step_type()))?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct CheckFinanceTerms;

impl Step for CheckFinanceTerms {
    type Input = DocumentDraft;

    fn options(&self) -> StepOptions<Self::Input> {
        live_branch_lock_options()
    }

    fn execute(&self, context: &mut Context, draft: Self::Input) -> HandlerResult<StepDecision> {
        if draft.revision < draft.plan.finance_rework_rounds {
            return Ok(StepDecision::go_to(
                &DraftDocument,
                DocumentDraft {
                    revision: draft.revision + 1,
                    plan: draft.plan,
                },
            ));
        }
        if draft.plan.is_finance_archived {
            change_live_branch_count(context, -1)?;
            return Ok(StepDecision::go_to(&ArchiveDocument, ()));
        }
        record_merge_arrival(context, format!("{}-{}", self.step_type(), draft.revision))?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct ArchiveDocument;

impl Step for ArchiveDocument {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct PublishDocument;

impl Step for PublishDocument {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        live_branch_lock_options()
    }

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(MERGE_SENTINEL.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let merged_reviews = MERGED_REVIEWS.get(context)?.unwrap_or_default();
        Ok(StepDecision::graceful_complete(sorted(merged_reviews)))
    }
}
