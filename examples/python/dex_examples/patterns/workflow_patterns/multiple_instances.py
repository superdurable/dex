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

from dataclasses import dataclass, replace
from typing import Any, Callable

from dex import (
    Attribute,
    AttributeMap,
    Channel,
    Context,
    Flow,
    Join,
    JoinRound,
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


# WCP-13 and WCP-14: the count is known when instances start, so a Join joins them.

# Each instance marks itself started, then waits at a gate, so all run at once.

STARTED_REVIEWERS = AttributeMap("startedReviewers", bool)
REVIEW_GATE_OPENED = Channel("reviewGateOpened", str)
REVIEW_COMPLETED = Join("reviewCompleted", str)
DESIGN_TIME_REVIEWERS = ("legal", "finance", "security")


def open_gate(context: Context, gate: Channel[str], count: int) -> None:
    for _ in range(count):
        gate.publish(context, "opened")


@dataclass(frozen=True)
class ReviewAssignment:
    reviewer: str
    join_round: JoinRound


def build_review(reviewer: str) -> Callable[[JoinRound], StepMovement[ReviewAssignment]]:
    return lambda join_round: StepMovement.of(
        ReviewDocument, ReviewAssignment(reviewer, join_round)
    )


def start_reviews(context: Context, reviewers: list[str]) -> StepDecision:
    return go_to_many(
        *REVIEW_COMPLETED.fork(
            context,
            [build_review(reviewer) for reviewer in reviewers],
            lambda join_round: StepMovement.of(CollectReviews, join_round),
        )
    )


class ReviewDocument(Step[ReviewAssignment]):
    def wait_for(self, context: Context, input: ReviewAssignment) -> Wait:
        STARTED_REVIEWERS.set(context, input.reviewer, True)
        return Wait.until(REVIEW_GATE_OPENED.for_one())

    def execute(self, context: Context, input: ReviewAssignment) -> StepDecision:
        return REVIEW_COMPLETED.end_branch(context, input.join_round, input.reviewer)


class CollectReviews(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(REVIEW_COMPLETED.for_all_branches(input))

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return graceful_complete(sorted(REVIEW_COMPLETED.branch_results(context, input)))


class StartDesignTimeReviews(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return start_reviews(context, list(DESIGN_TIME_REVIEWERS))


class StartRunTimeReviews(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return start_reviews(context, input)


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
        return PersistenceSchema.of(STARTED_REVIEWERS, REVIEW_GATE_OPENED, REVIEW_COMPLETED)

    @rpc(load_attribute_maps=(STARTED_REVIEWERS,))
    def get_started_design_time_review_count(self, context: Context) -> RPCResult[int]:
        return RPCResult(STARTED_REVIEWERS.get_map_size(context))

    @rpc
    def open_design_time_review_gate(self, context: Context, input: int) -> None:
        open_gate(context, REVIEW_GATE_OPENED, input)


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
        return PersistenceSchema.of(STARTED_REVIEWERS, REVIEW_GATE_OPENED, REVIEW_COMPLETED)

    @rpc(load_attribute_maps=(STARTED_REVIEWERS,))
    def get_started_run_time_review_count(self, context: Context) -> RPCResult[int]:
        return RPCResult(STARTED_REVIEWERS.get_map_size(context))

    @rpc
    def open_run_time_review_gate(self, context: Context, input: int) -> None:
        open_gate(context, REVIEW_GATE_OPENED, input)


# WCP-15: an RPC adds instances. The join re-waits until intake closes and all report.

# The count is final once intake closes, so the join needs no lock.

STARTED_SAMPLE_COUNT = Attribute("startedSampleCount", int)
IS_SAMPLE_INTAKE_CLOSED = Attribute("isSampleIntakeClosed", bool)
ANALYSIS_STARTED_SAMPLES = AttributeMap("analysisStartedSamples", bool)
SAMPLE_ANALYZED = Channel("sampleAnalyzed", str)
SAMPLE_INTAKE_CLOSED = Channel("sampleIntakeClosed", str)
SAMPLE_GATE_OPENED = Channel("sampleGateOpened", str)


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
    def wait_for(self, context: Context, input: str) -> Wait:
        ANALYSIS_STARTED_SAMPLES.set(context, input, True)
        return Wait.until(SAMPLE_GATE_OPENED.for_one())

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
            ANALYSIS_STARTED_SAMPLES,
            SAMPLE_ANALYZED,
            SAMPLE_INTAKE_CLOSED,
            SAMPLE_GATE_OPENED,
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

    @rpc(load_attribute_maps=(ANALYSIS_STARTED_SAMPLES,))
    def get_started_analysis_count(self, context: Context) -> RPCResult[int]:
        return RPCResult(ANALYSIS_STARTED_SAMPLES.get_map_size(context))

    @rpc
    def open_sample_gate(self, context: Context, input: int) -> None:
        open_gate(context, SAMPLE_GATE_OPENED, input)


# WCP-34: instances report to their round of a Join; the next round's split resets the
# previous round.


@dataclass(frozen=True)
class InspectionRound:
    inspection_count: int
    required_inspections: int
    round: int
    round_count: int
    previous_round: JoinRound | None = None


@dataclass(frozen=True)
class InspectionJoin:
    inspection_round: InspectionRound
    join_round: JoinRound


@dataclass(frozen=True)
class UnitInspection:
    unit_index: int
    join_round: JoinRound


@dataclass(frozen=True)
class InspectedUnits:
    round: int
    unit_indexes: list[int]


INSPECTION_RELEASED = Channel("inspectionReleased", str)
UNIT_INSPECTED = Join("unitInspected", int)
# The ignored late inspections of the most recently reset round.
IGNORED_INSPECTIONS = Attribute("ignoredInspections", InspectedUnits)


def build_unit_inspection(unit_index: int) -> Callable[[JoinRound], StepMovement[UnitInspection]]:
    return lambda join_round: StepMovement.of(
        InspectUnit, UnitInspection(unit_index, join_round)
    )


class StartInspections(Step[InspectionRound]):
    def wait_for(self, context: Context, input: InspectionRound) -> Wait:
        if input.previous_round is None:
            return Wait.skip_immediately()
        return Wait.until(
            UNIT_INSPECTED.for_remaining_branches(
                input.previous_round, input.required_inspections
            )
        )

    def execute(self, context: Context, input: InspectionRound) -> StepDecision:
        if input.previous_round is not None:
            ignored_units = sorted(
                UNIT_INSPECTED.remaining_branch_results(context, input.previous_round)
            )
            IGNORED_INSPECTIONS.set(
                context, InspectedUnits(input.round - 1, ignored_units)
            )
        return go_to_many(
            *UNIT_INSPECTED.fork(
                context,
                [
                    build_unit_inspection(unit_index)
                    for unit_index in range(input.inspection_count)
                ],
                lambda join_round: StepMovement.of(
                    AcceptInspections, InspectionJoin(input, join_round)
                ),
            )
        )


class InspectUnit(Step[UnitInspection]):
    def wait_for(self, context: Context, input: UnitInspection) -> Wait:
        return Wait.until(INSPECTION_RELEASED.for_one())

    def execute(self, context: Context, input: UnitInspection) -> StepDecision:
        return UNIT_INSPECTED.end_branch(context, input.join_round, input.unit_index)


class AcceptInspections(Step[InspectionJoin]):
    def wait_for(self, context: Context, input: InspectionJoin) -> Wait:
        return Wait.until(
            UNIT_INSPECTED.for_branches(
                input.join_round, input.inspection_round.required_inspections
            )
        )

    def execute(self, context: Context, input: InspectionJoin) -> StepDecision:
        inspection_round = input.inspection_round
        approved = InspectedUnits(
            inspection_round.round,
            sorted(UNIT_INSPECTED.branch_results(context, input.join_round)),
        )
        movements: list[StepMovement[Any]] = [StepMovement.of(ApproveBatch, approved)]
        if inspection_round.round < inspection_round.round_count:
            next_round = replace(
                inspection_round,
                round=inspection_round.round + 1,
                previous_round=input.join_round,
            )
            movements.append(StepMovement.of(StartInspections, next_round))
        return go_to_many(*movements)


class ApproveBatch(Step[InspectedUnits]):
    def execute(self, context: Context, input: InspectedUnits) -> StepDecision:
        return graceful_complete(input)


class StaticPartialJoinForMultipleInstancesFlow(Flow[InspectionRound]):
    def __init__(self) -> None:
        self.start_inspections = StartInspections()
        self.inspect_unit = InspectUnit()
        self.accept_inspections = AcceptInspections()
        self.approve_batch = ApproveBatch()

    def get_steps(self) -> StepList[InspectionRound]:
        return StepList.start_step(self.start_inspections).other_steps(
            self.inspect_unit,
            self.accept_inspections,
            self.approve_batch,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(INSPECTION_RELEASED, UNIT_INSPECTED, IGNORED_INSPECTIONS)

    @rpc
    def release_inspections(self, context: Context, input: int) -> None:
        for _ in range(input):
            INSPECTION_RELEASED.publish(context, "released")


# WCP-35: the join cancels the instances that have not finished.


@dataclass(frozen=True)
class PartialInspection:
    inspection_count: int
    required_inspections: int


QUORUM_INSPECTION_RELEASED = Channel("quorumInspectionReleased", str)
QUORUM_UNIT_INSPECTED = Join("quorumUnitInspected", int)
QUORUM_CERTIFIED = Channel("quorumCertified", str)


@dataclass(frozen=True)
class QuorumUnitInspection:
    unit_index: int
    join_round: JoinRound


@dataclass(frozen=True)
class QuorumJoin:
    required_inspections: int
    join_round: JoinRound


def build_quorum_unit_inspection(
    unit_index: int,
) -> Callable[[JoinRound], StepMovement[QuorumUnitInspection]]:
    return lambda join_round: StepMovement.of(
        InspectQuorumUnit, QuorumUnitInspection(unit_index, join_round)
    )


class StartQuorumInspections(Step[PartialInspection]):
    def execute(self, context: Context, input: PartialInspection) -> StepDecision:
        return go_to_many(
            *QUORUM_UNIT_INSPECTED.fork(
                context,
                [
                    build_quorum_unit_inspection(unit_index)
                    for unit_index in range(input.inspection_count)
                ],
                lambda join_round: StepMovement.of(
                    AcceptQuorumInspections,
                    QuorumJoin(input.required_inspections, join_round),
                ),
            )
        )


class InspectQuorumUnit(Step[QuorumUnitInspection]):
    def wait_for(self, context: Context, input: QuorumUnitInspection) -> Wait:
        return Wait.until(QUORUM_INSPECTION_RELEASED.for_one())

    def execute(self, context: Context, input: QuorumUnitInspection) -> StepDecision:
        return QUORUM_UNIT_INSPECTED.end_branch(context, input.join_round, input.unit_index)


class AcceptQuorumInspections(Step[QuorumJoin]):
    def wait_for(self, context: Context, input: QuorumJoin) -> Wait:
        return Wait.until(
            QUORUM_UNIT_INSPECTED.for_branches(input.join_round, input.required_inspections)
        )

    def execute(self, context: Context, input: QuorumJoin) -> StepDecision:
        return go_to(
            CertifyQuorum,
            sorted(QUORUM_UNIT_INSPECTED.branch_results(context, input.join_round)),
        ).with_canceling_sibling_steps(InspectQuorumUnit)


class CertifyQuorum(Step[list[int]]):
    def wait_for(self, context: Context, input: list[int]) -> Wait:
        return Wait.until(QUORUM_CERTIFIED.for_one())

    def execute(self, context: Context, input: list[int]) -> StepDecision:
        return graceful_complete(input)


class CancellingPartialJoinForMultipleInstancesFlow(Flow[PartialInspection]):
    def __init__(self) -> None:
        self.start_quorum_inspections = StartQuorumInspections()
        self.inspect_quorum_unit = InspectQuorumUnit()
        self.accept_quorum_inspections = AcceptQuorumInspections()
        self.certify_quorum_step = CertifyQuorum()

    def get_steps(self) -> StepList[PartialInspection]:
        return StepList.start_step(self.start_quorum_inspections).other_steps(
            self.inspect_quorum_unit,
            self.accept_quorum_inspections,
            self.certify_quorum_step,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            QUORUM_INSPECTION_RELEASED, QUORUM_UNIT_INSPECTED, QUORUM_CERTIFIED
        )

    @rpc
    def release_quorum_inspections(self, context: Context, input: int) -> None:
        for _ in range(input):
            QUORUM_INSPECTION_RELEASED.publish(context, "released")

    # A canceled instance no longer waits, so its late release stays queued.
    @rpc(load_channels=(QUORUM_INSPECTION_RELEASED,))
    def get_queued_quorum_release_count(self, context: Context) -> RPCResult[int]:
        return RPCResult(QUORUM_INSPECTION_RELEASED.size(context))

    @rpc
    def certify_quorum(self, context: Context) -> None:
        QUORUM_CERTIFIED.publish(context, "certified")


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
EVALUATION_STARTED_BIDS = AttributeMap("evaluationStartedBids", bool)
BID_EVALUATED = Channel("bidEvaluated", EvaluatedBid)
BID_GATE_OPENED = Channel("bidGateOpened", str)
CONTRACT_SIGNED = Channel("contractSigned", str)


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
    def wait_for(self, context: Context, input: int) -> Wait:
        EVALUATION_STARTED_BIDS.set(context, str(input), True)
        return Wait.until(BID_GATE_OPENED.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        BID_EVALUATED.publish(
            context, EvaluatedBid(input, input <= MAXIMUM_ACCEPTED_BID_AMOUNT)
        )
        return dead_end()


class AwardContract(Step[list[int]]):
    def wait_for(self, context: Context, input: list[int]) -> Wait:
        return Wait.until(CONTRACT_SIGNED.for_one())

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
        return PersistenceSchema.of(
            IS_BID_ADMISSION_CLOSED,
            EVALUATION_STARTED_BIDS,
            BID_EVALUATED,
            BID_GATE_OPENED,
            CONTRACT_SIGNED,
        )

    @rpc(lock_attributes=(IS_BID_ADMISSION_CLOSED.lock(),))
    def add_bid(self, context: Context, input: int) -> RPCResult[bool]:
        if IS_BID_ADMISSION_CLOSED.get(context):
            return RPCResult(False)
        return RPCResult(True, next_steps=(StepMovement.of(EvaluateBid, input),))

    @rpc(load_attribute_maps=(EVALUATION_STARTED_BIDS,))
    def get_started_evaluation_count(self, context: Context) -> RPCResult[int]:
        return RPCResult(EVALUATION_STARTED_BIDS.get_map_size(context))

    @rpc
    def open_bid_gate(self, context: Context, input: int) -> None:
        open_gate(context, BID_GATE_OPENED, input)

    @rpc
    def sign_contract(self, context: Context) -> None:
        CONTRACT_SIGNED.publish(context, "signed")
