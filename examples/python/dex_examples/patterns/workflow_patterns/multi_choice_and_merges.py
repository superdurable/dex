# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Workflow Patterns WCP-6, WCP-7, WCP-37, WCP-8, and WCP-38: choices and merges."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    Wait,
    dead_end,
    go_to,
    go_to_many,
    graceful_complete,
)

# WCP-6 Multi-Choice, WCP-7 and WCP-37 synchronizing merges: the join gets the count.


@dataclass(frozen=True)
class NotificationChoice:
    send_email: bool
    send_sms: bool
    send_push: bool


NOTIFICATION_SENT = Channel("notificationSent", str)


class SelectNotificationChannels(Step[NotificationChoice]):
    def execute(self, context: Context, input: NotificationChoice) -> StepDecision:
        selected: list[type[Step[None]]] = []
        if input.send_email:
            selected.append(SendEmail)
        if input.send_sms:
            selected.append(SendSms)
        if input.send_push:
            selected.append(SendPush)
        if not selected:
            return graceful_complete([])
        movements: list[StepMovement[Any]] = [
            StepMovement.of(SynchronizingMerge, len(selected))
        ]
        movements.extend(StepMovement.of(branch, None) for branch in selected)
        return go_to_many(*movements)


class SendEmail(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        NOTIFICATION_SENT.publish(context, "SendEmail")
        return dead_end()


class SendSms(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        NOTIFICATION_SENT.publish(context, "SendSms")
        return dead_end()


class SendPush(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        NOTIFICATION_SENT.publish(context, "SendPush")
        return dead_end()


class SynchronizingMerge(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(NOTIFICATION_SENT.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(sorted(NOTIFICATION_SENT.results(context)))


class MultiChoiceSynchronizingMergeFlow(Flow[NotificationChoice]):
    def __init__(self) -> None:
        self.select_notification_channels = SelectNotificationChannels()
        self.send_email = SendEmail()
        self.send_sms = SendSms()
        self.send_push = SendPush()
        self.synchronizing_merge = SynchronizingMerge()

    def get_steps(self) -> StepList[NotificationChoice]:
        return StepList.start_step(self.select_notification_channels).other_steps(
            self.send_email, self.send_sms, self.send_push, self.synchronizing_merge
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(NOTIFICATION_SENT)


# WCP-8 Multi-Merge: every arrival at RecordReview starts a new execution.


class StartReviews(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to_many(
            StepMovement.of(LegalReview, None), StepMovement.of(FinanceReview, None)
        )


class LegalReview(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(RecordReview, "LegalReview")


class FinanceReview(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(RecordReview, "FinanceReview")


class RecordReview(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(input)


class MultiMergeFlow(Flow[None]):
    def __init__(self) -> None:
        self.start_reviews = StartReviews()
        self.legal_review = LegalReview()
        self.finance_review = FinanceReview()
        self.record_review = RecordReview()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_reviews).other_steps(
            self.legal_review, self.finance_review, self.record_review
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()


# WCP-38: an Attribute counts the threads that can still reach PublishDocument.

# The commit that brings the count to 0 publishes the sentinel the merge waits for.


@dataclass(frozen=True)
class DocumentReviewPlan:
    is_legal_check_needed: bool
    is_finance_check_needed: bool
    finance_rework_rounds: int
    is_finance_archived: bool


@dataclass(frozen=True)
class DocumentDraft:
    plan: DocumentReviewPlan
    revision: int


LIVE_BRANCH_COUNT = Attribute("liveBranchCount", int)
MERGED_REVIEWS = Attribute("mergedReviews", list[str])
MERGE_SENTINEL = Channel("mergeSentinel", str)
LIVE_BRANCH_COUNT_LOCK = StepOptions(execute_lock_attributes=(LIVE_BRANCH_COUNT.lock(),))


# Call only from a Step that holds the liveBranchCount lock.
def change_live_branch_count(context: Context, delta: int) -> None:
    count = LIVE_BRANCH_COUNT.get(context) + delta
    LIVE_BRANCH_COUNT.set(context, count)
    if count == 0:
        MERGE_SENTINEL.publish(context, "done")


def record_arrival(context: Context, review: str) -> None:
    MERGED_REVIEWS.set(context, [*(MERGED_REVIEWS.get(context) or []), review])
    change_live_branch_count(context, -1)


class StartDocumentReview(Step[DocumentReviewPlan]):
    def get_step_options(self) -> StepOptions:
        return LIVE_BRANCH_COUNT_LOCK

    def execute(self, context: Context, input: DocumentReviewPlan) -> StepDecision:
        LIVE_BRANCH_COUNT.set(context, 1)
        return go_to_many(
            StepMovement.of(DraftDocument, DocumentDraft(input, 0)),
            StepMovement.of(PublishDocument, None),
        )


class DraftDocument(Step[DocumentDraft]):
    def get_step_options(self) -> StepOptions:
        return LIVE_BRANCH_COUNT_LOCK

    def execute(self, context: Context, input: DocumentDraft) -> StepDecision:
        is_rework = input.revision > 0
        movements: list[StepMovement[Any]] = []
        if is_rework or input.plan.is_legal_check_needed:
            movements.append(StepMovement.of(CheckLegalTerms, input.revision))
        if is_rework or input.plan.is_finance_check_needed:
            movements.append(StepMovement.of(CheckFinanceTerms, input))
        change_live_branch_count(context, len(movements) - 1)
        if not movements:
            return dead_end()
        return go_to_many(*movements)


class CheckLegalTerms(Step[int]):
    def get_step_options(self) -> StepOptions:
        return LIVE_BRANCH_COUNT_LOCK

    def execute(self, context: Context, input: int) -> StepDecision:
        record_arrival(context, f"CheckLegalTerms-{input}")
        return dead_end()


class CheckFinanceTerms(Step[DocumentDraft]):
    def get_step_options(self) -> StepOptions:
        return LIVE_BRANCH_COUNT_LOCK

    def execute(self, context: Context, input: DocumentDraft) -> StepDecision:
        if input.revision < input.plan.finance_rework_rounds:
            return go_to(DraftDocument, DocumentDraft(input.plan, input.revision + 1))
        if input.plan.is_finance_archived:
            change_live_branch_count(context, -1)
            return go_to(ArchiveDocument, None)
        record_arrival(context, f"CheckFinanceTerms-{input.revision}")
        return dead_end()


class ArchiveDocument(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("ArchiveDocument")


class PublishDocument(Step[None]):
    def get_step_options(self) -> StepOptions:
        return LIVE_BRANCH_COUNT_LOCK

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(MERGE_SENTINEL.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete(sorted(MERGED_REVIEWS.get(context) or []))


class GeneralSynchronizingMergeFlow(Flow[DocumentReviewPlan]):
    def __init__(self) -> None:
        self.start_document_review = StartDocumentReview()
        self.draft_document = DraftDocument()
        self.check_legal_terms = CheckLegalTerms()
        self.check_finance_terms = CheckFinanceTerms()
        self.archive_document = ArchiveDocument()
        self.publish_document = PublishDocument()

    def get_steps(self) -> StepList[DocumentReviewPlan]:
        return StepList.start_step(self.start_document_review).other_steps(
            self.draft_document,
            self.check_legal_terms,
            self.check_finance_terms,
            self.archive_document,
            self.publish_document,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(LIVE_BRANCH_COUNT, MERGED_REVIEWS, MERGE_SENTINEL)
