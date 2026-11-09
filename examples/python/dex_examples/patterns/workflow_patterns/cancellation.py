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

"""Cancellation Workflow Patterns: WCP-19, WCP-20, and WCP-25 to WCP-27."""

from __future__ import annotations

from typing import Any, Callable

from dex import (
    AsyncClient,
    AsyncContext,
    Attribute,
    Channel,
    Context,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    StopFlowOptions,
    StopType,
    SubFlow,
    Wait,
    dead_end,
    force_fail,
    go_to,
    go_to_many,
    graceful_complete,
    rpc,
)

# WCP-19 Cancel Task: an RPC withdraws the waiting pickup and starts another Step.

COURIER_ARRIVED = Channel("courierArrived", str)


class AwaitCourierPickup(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(COURIER_ARRIVED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("AwaitCourierPickup")


class ArrangeSelfDelivery(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("ArrangeSelfDelivery")


class CancelTaskFlow(Flow[None]):
    def __init__(self) -> None:
        self.await_courier_pickup = AwaitCourierPickup()
        self.arrange_self_delivery = ArrangeSelfDelivery()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.await_courier_pickup).other_steps(
            self.arrange_self_delivery
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(COURIER_ARRIVED)

    @rpc
    def confirm_courier_pickup(self, context: Context) -> None:
        COURIER_ARRIVED.publish(context, "arrived")

    @rpc
    def cancel_courier_pickup(self, context: Context) -> RPCResult[None]:
        return RPCResult(
            None, next_steps=(StepMovement.of(ArrangeSelfDelivery, None),)
        ).with_canceling_steps(AwaitCourierPickup)


# WCP-20 Cancel Case: Client StopFlow alone leaves a running SubFlow alive.

# So the Step that owns the child also waits for cancelCase and stops the child itself.

STOPPED_CHILD_FLOW_IDS = Attribute("stoppedChildFlowIds", list[str])
APPLICATION_REVIEWED = Channel("applicationReviewed", str)
CANCEL_CASE = Channel("cancelCase", str)
BACKGROUND_CHECK_COMPLETED = Channel("backgroundCheckCompleted", str)


class AwaitBackgroundCheck(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(BACKGROUND_CHECK_COMPLETED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("AwaitBackgroundCheck")


class BackgroundCheckFlow(Flow[None]):
    def __init__(self) -> None:
        self.await_background_check = AwaitBackgroundCheck()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.await_background_check)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(BACKGROUND_CHECK_COMPLETED)


class StartApplication(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to_many(
            StepMovement.of(ReviewApplication, None),
            StepMovement.of(RunBackgroundCheck, None),
        )


class ReviewApplication(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(APPLICATION_REVIEWED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("ReviewApplication")


class RunBackgroundCheck(Step[None]):
    def __init__(
        self,
        background_check_flow: BackgroundCheckFlow,
        client_provider: Callable[[], AsyncClient],
    ) -> None:
        self.background_check_flow = background_check_flow
        self.client_provider = client_provider

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.any_of(
            SubFlow.run(self.background_check_flow, None), CANCEL_CASE.for_one()
        )

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        cancel_reasons = CANCEL_CASE.results(context)
        if not cancel_reasons:
            return go_to(WatchCaseCancellation, None)
        # The child keeps running after this AnyOf; its ID is still readable here.
        child_flow_id = SubFlow.get_flow_id(context)
        await stop_flow_if_active(self.client_provider(), child_flow_id)
        STOPPED_CHILD_FLOW_IDS.set(context, [child_flow_id])
        return force_fail(f"case canceled: {cancel_reasons[0]}")


class WatchCaseCancellation(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(CANCEL_CASE.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return force_fail(f"case canceled: {CANCEL_CASE.results(context)[0]}")


# A retried Execute stops the same child again; a closed child counts as stopped.
async def stop_flow_if_active(client: AsyncClient, flow_id: str) -> None:
    try:
        await client.stop_flow(
            flow_id, StopFlowOptions(type=StopType.CANCEL, reason="parent case canceled")
        )
    except (FlowNotFoundError, FlowNotActiveOrNotFoundError):
        return


class CancelCaseFlow(Flow[None]):
    def __init__(
        self,
        background_check_flow: BackgroundCheckFlow,
        client_provider: Callable[[], AsyncClient],
    ) -> None:
        self.start_application = StartApplication()
        self.review_application = ReviewApplication()
        self.run_background_check = RunBackgroundCheck(
            background_check_flow, client_provider
        )
        self.watch_case_cancellation = WatchCaseCancellation()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_application).other_steps(
            self.review_application,
            self.run_background_check,
            self.watch_case_cancellation,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            STOPPED_CHILD_FLOW_IDS, APPLICATION_REVIEWED, CANCEL_CASE
        )

    @rpc
    def cancel_case(self, context: Context, input: str) -> None:
        CANCEL_CASE.publish(context, input)


# WCP-25 Cancel Region: one RPC withdraws two Step types and keeps the third.

IDENTITY_VERIFIED = Channel("identityVerified", str)
CREDIT_HISTORY_CHECKED = Channel("creditHistoryChecked", str)
SANCTIONS_SCREENED = Channel("sanctionsScreened", str)


class StartApplicationChecks(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to_many(
            StepMovement.of(VerifyIdentity, None),
            StepMovement.of(CheckCreditHistory, None),
            StepMovement.of(ScreenSanctions, None),
        )


class VerifyIdentity(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(IDENTITY_VERIFIED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("VerifyIdentity")


class CheckCreditHistory(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(CREDIT_HISTORY_CHECKED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("CheckCreditHistory")


class ScreenSanctions(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(SANCTIONS_SCREENED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("ScreenSanctions")


class NotifyRejection(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("NotifyRejection")


class CancelRegionFlow(Flow[None]):
    def __init__(self) -> None:
        self.start_application_checks = StartApplicationChecks()
        self.verify_identity = VerifyIdentity()
        self.check_credit_history = CheckCreditHistory()
        self.screen_sanctions = ScreenSanctions()
        self.notify_rejection = NotifyRejection()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_application_checks).other_steps(
            self.verify_identity,
            self.check_credit_history,
            self.screen_sanctions,
            self.notify_rejection,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            IDENTITY_VERIFIED, CREDIT_HISTORY_CHECKED, SANCTIONS_SCREENED
        )

    @rpc
    def reject_application(self, context: Context) -> RPCResult[None]:
        return RPCResult(
            None, next_steps=(StepMovement.of(NotifyRejection, None),)
        ).with_canceling_steps(VerifyIdentity, CheckCreditHistory)

    @rpc
    def complete_sanctions_screening(self, context: Context) -> None:
        SANCTIONS_SCREENED.publish(context, "screened")


# WCP-26 Cancel Multiple Instance Activity: canceling the Step type withdraws
# unfinished instances; finished instances keep their results.

FINISHED_TRANSLATION_COUNT = Attribute("finishedTranslationCount", int)
TRANSLATION_RELEASED = Channel("translationReleased", str)


class StartTranslations(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to_many(
            *(StepMovement.of(TranslateDocument, index) for index in range(input))
        )


class TranslateDocument(Step[int]):
    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_lock_attributes=(FINISHED_TRANSLATION_COUNT.lock(),))

    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(TRANSLATION_RELEASED.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        FINISHED_TRANSLATION_COUNT.set(
            context, FINISHED_TRANSLATION_COUNT.get(context) + 1
        )
        return graceful_complete(input)


class ReportTranslations(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete(FINISHED_TRANSLATION_COUNT.get(context))


class CancelMultipleInstanceTaskFlow(Flow[int]):
    def __init__(self) -> None:
        self.start_translations = StartTranslations()
        self.translate_document = TranslateDocument()
        self.report_translations = ReportTranslations()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start_translations).other_steps(
            self.translate_document, self.report_translations
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(FINISHED_TRANSLATION_COUNT, TRANSLATION_RELEASED)

    @rpc
    def release_translation(self, context: Context) -> None:
        TRANSLATION_RELEASED.publish(context, "released")

    @rpc
    def cancel_translations(self, context: Context) -> RPCResult[None]:
        return RPCResult(
            None, next_steps=(StepMovement.of(ReportTranslations, None),)
        ).with_canceling_steps(TranslateDocument)


# WCP-27: an RPC withdraws the open instances and the join, then continues.

PEER_REVIEW_RELEASED = Channel("peerReviewReleased", str)
PEER_REVIEW_SUBMITTED = Channel("peerReviewSubmitted", int)


class StartPeerReviews(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        movements: list[StepMovement[Any]] = [
            StepMovement.of(SubmitPeerReview, index) for index in range(input)
        ]
        movements.append(StepMovement.of(CollectPeerReviews, input))
        return go_to_many(*movements)


class SubmitPeerReview(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(PEER_REVIEW_RELEASED.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        PEER_REVIEW_SUBMITTED.publish(context, input)
        return dead_end()


class CollectPeerReviews(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(PEER_REVIEW_SUBMITTED.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to(PublishReviewResults, sorted(PEER_REVIEW_SUBMITTED.results(context)))


class PublishReviewResults(Step[list[int]]):
    def execute(self, context: Context, input: list[int]) -> StepDecision:
        return graceful_complete(input)


class CompleteMultipleInstanceTaskFlow(Flow[int]):
    def __init__(self) -> None:
        self.start_peer_reviews = StartPeerReviews()
        self.submit_peer_review = SubmitPeerReview()
        self.collect_peer_reviews = CollectPeerReviews()
        self.publish_review_results = PublishReviewResults()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start_peer_reviews).other_steps(
            self.submit_peer_review,
            self.collect_peer_reviews,
            self.publish_review_results,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(PEER_REVIEW_RELEASED, PEER_REVIEW_SUBMITTED)

    @rpc
    def release_peer_review(self, context: Context) -> None:
        PEER_REVIEW_RELEASED.publish(context, "released")

    @rpc(load_channels=(PEER_REVIEW_SUBMITTED,))
    def get_submitted_peer_review_count(self, context: Context) -> RPCResult[int]:
        return RPCResult(PEER_REVIEW_SUBMITTED.size(context))

    @rpc(load_channels=(PEER_REVIEW_SUBMITTED,))
    def complete_peer_reviews_early(self, context: Context) -> RPCResult[None]:
        submitted_indexes = sorted(
            message.value for message in PEER_REVIEW_SUBMITTED.pending_messages(context)
        )
        return RPCResult(
            None,
            next_steps=(StepMovement.of(PublishReviewResults, submitted_indexes),),
        ).with_canceling_steps(SubmitPeerReview, CollectPeerReviews)
