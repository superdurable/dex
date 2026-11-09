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
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    SubFlow,
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


# WCP-38 General Synchronizing Merge: the region runs as a SubFlow that completes
# once none of its Steps can run.


class DraftProposal(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to_many(
            StepMovement.of(ReviewProposal, input),
            StepMovement.of(TranslateProposal, input),
        )


class ReviewProposal(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        if input < 1:
            return go_to(DraftProposal, input + 1)
        return graceful_complete(f"ReviewProposal-{input}")


class TranslateProposal(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(f"TranslateProposal-{input}")


class ReviewRegionFlow(Flow[int]):
    def __init__(self) -> None:
        self.draft_proposal = DraftProposal()
        self.review_proposal = ReviewProposal()
        self.translate_proposal = TranslateProposal()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.draft_proposal).other_steps(
            self.review_proposal, self.translate_proposal
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()


class RunReviewRegion(Step[None]):
    def __init__(self, review_region_flow: ReviewRegionFlow) -> None:
        self.review_region_flow = review_region_flow

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(SubFlow.run(self.review_region_flow, 0))

    def execute(self, context: Context, input: None) -> StepDecision:
        region_result = SubFlow.get_condition_results(context)
        region_outputs = [
            completion.decode(str) for completion in region_result.completions
        ]
        return go_to(PublishDecision, sorted(region_outputs))


class PublishDecision(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return graceful_complete(input)


class GeneralSynchronizingMergeFlow(Flow[None]):
    def __init__(self, review_region_flow: ReviewRegionFlow) -> None:
        self.run_review_region = RunReviewRegion(review_region_flow)
        self.publish_decision = PublishDecision()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.run_review_region).other_steps(
            self.publish_decision
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
