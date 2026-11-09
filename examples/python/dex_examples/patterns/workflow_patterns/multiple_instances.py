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

"""Multiple instances of one task: WCP-12 to WCP-15 and WCP-34 to WCP-36."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
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
    rpc,
)

# WCP-12 Multiple Instances without Synchronization: nothing waits for invitations.

INVITATION_RELEASED = Channel("invitationReleased", str)


class RegisterAttendees(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        movements: list[StepMovement[Any]] = [
            StepMovement.of(SendInvitation, attendee) for attendee in input
        ]
        movements.append(StepMovement.of(ConfirmRegistration, None))
        return go_to_many(*movements)


class SendInvitation(Step[str]):
    def wait_for(self, context: Context, input: str) -> Wait:
        return Wait.until(INVITATION_RELEASED.for_one())

    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(input)


class ConfirmRegistration(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("ConfirmRegistration")


class MultipleInstancesWithoutSynchronizationFlow(Flow[list[str]]):
    def __init__(self) -> None:
        self.register_attendees = RegisterAttendees()
        self.send_invitation = SendInvitation()
        self.confirm_registration = ConfirmRegistration()

    def get_steps(self) -> StepList[list[str]]:
        return StepList.start_step(self.register_attendees).other_steps(
            self.send_invitation, self.confirm_registration
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(INVITATION_RELEASED)

    @rpc
    def release_invitations(self, context: Context, input: int) -> None:
        for _ in range(input):
            INVITATION_RELEASED.publish(context, "released")


# WCP-13 and WCP-14: the count is known when instances start, so ForN joins them.

REVIEW_COMPLETED = Channel("reviewCompleted", str)
DESIGN_TIME_REVIEWERS = ("legal", "finance", "security")


def start_reviews(reviewers: list[str]) -> StepDecision:
    movements: list[StepMovement[Any]] = [
        StepMovement.of(ReviewDocument, reviewer) for reviewer in reviewers
    ]
    movements.append(StepMovement.of(CollectReviews, len(reviewers)))
    return go_to_many(*movements)


class ReviewDocument(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        REVIEW_COMPLETED.publish(context, input)
        return dead_end()


class CollectReviews(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(REVIEW_COMPLETED.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(sorted(REVIEW_COMPLETED.results(context)))


class StartDesignTimeReviews(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return start_reviews(list(DESIGN_TIME_REVIEWERS))


class StartRunTimeReviews(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return start_reviews(input)


class DesignTimeMultipleInstancesFlow(Flow[None]):
    def __init__(self) -> None:
        self.start_design_time_reviews = StartDesignTimeReviews()
        self.review_document = ReviewDocument()
        self.collect_reviews = CollectReviews()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_design_time_reviews).other_steps(
            self.review_document, self.collect_reviews
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(REVIEW_COMPLETED)


class RunTimeMultipleInstancesFlow(Flow[list[str]]):
    def __init__(self) -> None:
        self.start_run_time_reviews = StartRunTimeReviews()
        self.review_document = ReviewDocument()
        self.collect_reviews = CollectReviews()

    def get_steps(self) -> StepList[list[str]]:
        return StepList.start_step(self.start_run_time_reviews).other_steps(
            self.review_document, self.collect_reviews
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(REVIEW_COMPLETED)


# WCP-15: an RPC adds instances while others run. The join re-waits until intake
# closes and every started sample reported.

STARTED_SAMPLE_COUNT = Attribute("startedSampleCount", int)
IS_SAMPLE_INTAKE_CLOSED = Attribute("isSampleIntakeClosed", bool)
SAMPLE_ANALYZED = Channel("sampleAnalyzed", str)
SAMPLE_INTAKE_CLOSED = Channel("sampleIntakeClosed", str)


class JoinSamples(Step[list[str]]):
    def wait_for(self, context: Context, input: list[str]) -> Wait:
        if not IS_SAMPLE_INTAKE_CLOSED.get(context):
            return Wait.any_of(SAMPLE_ANALYZED.for_one(), SAMPLE_INTAKE_CLOSED.for_one())
        if len(input) == STARTED_SAMPLE_COUNT.get(context):
            return Wait.skip_immediately()
        return Wait.until(SAMPLE_ANALYZED.for_one())

    def execute(self, context: Context, input: list[str]) -> StepDecision:
        analyzed_samples = [*input, *SAMPLE_ANALYZED.results(context)]
        is_every_sample_analyzed = len(analyzed_samples) == STARTED_SAMPLE_COUNT.get(
            context
        )
        if IS_SAMPLE_INTAKE_CLOSED.get(context) and is_every_sample_analyzed:
            return go_to(ReportSamples, analyzed_samples)
        return go_to(JoinSamples, analyzed_samples)


class AnalyzeSample(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        SAMPLE_ANALYZED.publish(context, input)
        return dead_end()


class ReportSamples(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return graceful_complete(sorted(input))


class NoPriorKnowledgeMultipleInstancesFlow(Flow[list[str]]):
    def __init__(self) -> None:
        self.join_samples = JoinSamples()
        self.analyze_sample = AnalyzeSample()
        self.report_samples = ReportSamples()

    def get_steps(self) -> StepList[list[str]]:
        return StepList.start_step(self.join_samples).other_steps(
            self.analyze_sample, self.report_samples
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            STARTED_SAMPLE_COUNT,
            IS_SAMPLE_INTAKE_CLOSED,
            SAMPLE_ANALYZED,
            SAMPLE_INTAKE_CLOSED,
        )

    @rpc(lock_attributes=(STARTED_SAMPLE_COUNT.lock(),))
    def add_sample(self, context: Context, input: str) -> RPCResult[bool]:
        if IS_SAMPLE_INTAKE_CLOSED.get(context):
            return RPCResult(False)
        STARTED_SAMPLE_COUNT.set(context, STARTED_SAMPLE_COUNT.get(context) + 1)
        return RPCResult(True, next_steps=(StepMovement.of(AnalyzeSample, input),))

    @rpc(lock_attributes=(STARTED_SAMPLE_COUNT.lock(),))
    def close_sample_intake(self, context: Context) -> None:
        IS_SAMPLE_INTAKE_CLOSED.set(context, True)
        SAMPLE_INTAKE_CLOSED.publish(context, "closed")


# WCP-34 Static Partial Join for Multiple Instances: ForN(required) continues and a
# reset Step consumes the remaining results.


@dataclass(frozen=True)
class PartialInspection:
    inspection_count: int
    required_inspections: int


INSPECTION_RELEASED = Channel("inspectionReleased", str)
UNIT_INSPECTED = Channel("unitInspected", int)


class StartInspections(Step[PartialInspection]):
    def execute(self, context: Context, input: PartialInspection) -> StepDecision:
        movements: list[StepMovement[Any]] = [
            StepMovement.of(InspectUnit, index) for index in range(input.inspection_count)
        ]
        movements.append(StepMovement.of(AcceptInspections, input))
        return go_to_many(*movements)


class InspectUnit(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(INSPECTION_RELEASED.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        UNIT_INSPECTED.publish(context, input)
        return dead_end()


class AcceptInspections(Step[PartialInspection]):
    def wait_for(self, context: Context, input: PartialInspection) -> Wait:
        return Wait.until(UNIT_INSPECTED.for_n(input.required_inspections))

    def execute(self, context: Context, input: PartialInspection) -> StepDecision:
        return go_to_many(
            StepMovement.of(ApproveBatch, sorted(UNIT_INSPECTED.results(context))),
            StepMovement.of(
                ResetInspectionJoin, input.inspection_count - input.required_inspections
            ),
        )


class ApproveBatch(Step[list[int]]):
    def execute(self, context: Context, input: list[int]) -> StepDecision:
        return graceful_complete(input)


class ResetInspectionJoin(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(UNIT_INSPECTED.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(sorted(UNIT_INSPECTED.results(context)))


class StaticPartialJoinForMultipleInstancesFlow(Flow[PartialInspection]):
    def __init__(self) -> None:
        self.start_inspections = StartInspections()
        self.inspect_unit = InspectUnit()
        self.accept_inspections = AcceptInspections()
        self.approve_batch = ApproveBatch()
        self.reset_inspection_join = ResetInspectionJoin()

    def get_steps(self) -> StepList[PartialInspection]:
        return StepList.start_step(self.start_inspections).other_steps(
            self.inspect_unit,
            self.accept_inspections,
            self.approve_batch,
            self.reset_inspection_join,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(INSPECTION_RELEASED, UNIT_INSPECTED)

    @rpc
    def release_inspections(self, context: Context, input: int) -> None:
        for _ in range(input):
            INSPECTION_RELEASED.publish(context, "released")


# WCP-35 Cancelling Partial Join for Multiple Instances: the join cancels the rest.

QUORUM_INSPECTION_RELEASED = Channel("quorumInspectionReleased", str)
QUORUM_UNIT_INSPECTED = Channel("quorumUnitInspected", int)


class StartQuorumInspections(Step[PartialInspection]):
    def execute(self, context: Context, input: PartialInspection) -> StepDecision:
        movements: list[StepMovement[Any]] = [
            StepMovement.of(InspectQuorumUnit, index)
            for index in range(input.inspection_count)
        ]
        movements.append(StepMovement.of(AcceptQuorumInspections, input.required_inspections))
        return go_to_many(*movements)


class InspectQuorumUnit(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(QUORUM_INSPECTION_RELEASED.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        QUORUM_UNIT_INSPECTED.publish(context, input)
        return dead_end()


class AcceptQuorumInspections(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(QUORUM_UNIT_INSPECTED.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(
            sorted(QUORUM_UNIT_INSPECTED.results(context))
        ).with_canceling_steps(InspectQuorumUnit)


class CancellingPartialJoinForMultipleInstancesFlow(Flow[PartialInspection]):
    def __init__(self) -> None:
        self.start_quorum_inspections = StartQuorumInspections()
        self.inspect_quorum_unit = InspectQuorumUnit()
        self.accept_quorum_inspections = AcceptQuorumInspections()

    def get_steps(self) -> StepList[PartialInspection]:
        return StepList.start_step(self.start_quorum_inspections).other_steps(
            self.inspect_quorum_unit, self.accept_quorum_inspections
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(QUORUM_INSPECTION_RELEASED, QUORUM_UNIT_INSPECTED)

    @rpc
    def release_quorum_inspections(self, context: Context, input: int) -> None:
        for _ in range(input):
            QUORUM_INSPECTION_RELEASED.publish(context, "released")


# WCP-36: the join tests its condition after each bid, then closes admission.

# A shared lock stops late bids; a conflicting RPC fails fast and the caller retries.


@dataclass(frozen=True)
class BidJoin:
    required_accepted_bids: int
    accepted_bids: list[int]


@dataclass(frozen=True)
class EvaluatedBid:
    amount: int
    is_accepted: bool


MAXIMUM_ACCEPTED_BID_AMOUNT = 1000
IS_BID_ADMISSION_CLOSED = Attribute("isBidAdmissionClosed", bool)
BID_EVALUATED = Channel("bidEvaluated", EvaluatedBid)


class JoinBids(Step[BidJoin]):
    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_lock_attributes=(IS_BID_ADMISSION_CLOSED.lock(),))

    def wait_for(self, context: Context, input: BidJoin) -> Wait:
        return Wait.until(BID_EVALUATED.for_one())

    def execute(self, context: Context, input: BidJoin) -> StepDecision:
        accepted_bids = [
            *input.accepted_bids,
            *(bid.amount for bid in BID_EVALUATED.results(context) if bid.is_accepted),
        ]
        if len(accepted_bids) >= input.required_accepted_bids:
            IS_BID_ADMISSION_CLOSED.set(context, True)
            return go_to(AwardContract, accepted_bids)
        return go_to(JoinBids, BidJoin(input.required_accepted_bids, accepted_bids))


class EvaluateBid(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        BID_EVALUATED.publish(
            context, EvaluatedBid(input, input <= MAXIMUM_ACCEPTED_BID_AMOUNT)
        )
        return dead_end()


class AwardContract(Step[list[int]]):
    def execute(self, context: Context, input: list[int]) -> StepDecision:
        return graceful_complete(sorted(input))


class DynamicPartialJoinForMultipleInstancesFlow(Flow[BidJoin]):
    def __init__(self) -> None:
        self.join_bids = JoinBids()
        self.evaluate_bid = EvaluateBid()
        self.award_contract = AwardContract()

    def get_steps(self) -> StepList[BidJoin]:
        return StepList.start_step(self.join_bids).other_steps(
            self.evaluate_bid, self.award_contract
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(IS_BID_ADMISSION_CLOSED, BID_EVALUATED)

    @rpc(lock_attributes=(IS_BID_ADMISSION_CLOSED.lock(),))
    def add_bid(self, context: Context, input: int) -> RPCResult[bool]:
        if IS_BID_ADMISSION_CLOSED.get(context):
            return RPCResult(False)
        return RPCResult(True, next_steps=(StepMovement.of(EvaluateBid, input),))
