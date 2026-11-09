/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Workflow Patterns for multiple instances of one task: WCP-12 to WCP-15 and
// WCP-34 to WCP-36.

import {
  Attribute,
  Channel,
  StepList,
  StepMovement,
  Wait,
  booleanCodec,
  deadEnd,
  doubleCodec,
  goTo,
  goToMany,
  gracefulComplete,
  jsonCodec,
  rpc,
  stringCodec,
  withCancelingSteps,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

function sortNumbers(values: readonly number[]): number[] {
  return [...values].sort((left, right) => left - right);
}

// WCP-12 Multiple Instances without Synchronization: nothing waits for the
// invitations, so ConfirmRegistration runs while they are still pending.

const invitationReleased = new Channel("invitationReleased", stringCodec);

class RegisterAttendees implements Step<string[]> {
  public getStepType(): string {
    return "RegisterAttendees";
  }

  public execute(_context: Context, attendees: string[]): StepDecision {
    return goToMany(
      ...attendees.map((attendee) => StepMovement.of(SendInvitation, attendee)),
      StepMovement.of(ConfirmRegistration, undefined),
    );
  }
}

class SendInvitation implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "SendInvitation";
  }

  public waitFor(_context: Context, _attendee: string): Wait {
    return Wait.until(invitationReleased.forOne());
  }

  public execute(_context: Context, attendee: string): StepDecision {
    return gracefulComplete(attendee);
  }
}

class ConfirmRegistration implements Step<void> {
  public getStepType(): string {
    return "ConfirmRegistration";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("ConfirmRegistration");
  }
}

export class MultipleInstancesWithoutSynchronizationFlow implements Flow<string[]> {
  private readonly registerAttendees = new RegisterAttendees();
  private readonly sendInvitation = new SendInvitation();
  private readonly confirmRegistration = new ConfirmRegistration();

  public getFlowType(): string {
    return "MultipleInstancesWithoutSynchronizationFlow";
  }

  public getSteps(): StepList<string[]> {
    return StepList.startStep(this.registerAttendees).otherSteps(
      this.sendInvitation,
      this.confirmRegistration,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [invitationReleased] };
  }

  @rpc({ inputCodec: doubleCodec })
  public releaseInvitations(context: Context, count: number): void {
    for (let index = 0; index < count; index++) {
      invitationReleased.publish(context, "released");
    }
  }
}

// WCP-13 and WCP-14: the count is known when instances start, so ForN joins them.

const reviewCompleted = new Channel("reviewCompleted", stringCodec);
const DESIGN_TIME_REVIEWERS = ["legal", "finance", "security"];

class ReviewDocument implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "ReviewDocument";
  }

  public execute(context: Context, reviewer: string): StepDecision {
    reviewCompleted.publish(context, reviewer);
    return deadEnd();
  }
}

class CollectReviews implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "CollectReviews";
  }

  public waitFor(_context: Context, reviewerCount: number): Wait {
    return Wait.until(reviewCompleted.forN(reviewerCount));
  }

  public execute(context: Context, _reviewerCount: number): StepDecision {
    return gracefulComplete([...reviewCompleted.results(context)].sort());
  }
}

function startReviews(reviewers: readonly string[]): StepDecision {
  return goToMany(
    ...reviewers.map((reviewer) => StepMovement.of(ReviewDocument, reviewer)),
    StepMovement.of(CollectReviews, reviewers.length),
  );
}

class StartDesignTimeReviews implements Step<void> {
  public getStepType(): string {
    return "StartDesignTimeReviews";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return startReviews(DESIGN_TIME_REVIEWERS);
  }
}

class StartRunTimeReviews implements Step<string[]> {
  public getStepType(): string {
    return "StartRunTimeReviews";
  }

  public execute(_context: Context, reviewers: string[]): StepDecision {
    return startReviews(reviewers);
  }
}

export class DesignTimeMultipleInstancesFlow implements Flow<void> {
  private readonly startDesignTimeReviews = new StartDesignTimeReviews();
  private readonly reviewDocument = new ReviewDocument();
  private readonly collectReviews = new CollectReviews();

  public getFlowType(): string {
    return "DesignTimeMultipleInstancesFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.startDesignTimeReviews).otherSteps(
      this.reviewDocument,
      this.collectReviews,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [reviewCompleted] };
  }
}

export class RunTimeMultipleInstancesFlow implements Flow<string[]> {
  private readonly startRunTimeReviews = new StartRunTimeReviews();
  private readonly reviewDocument = new ReviewDocument();
  private readonly collectReviews = new CollectReviews();

  public getFlowType(): string {
    return "RunTimeMultipleInstancesFlow";
  }

  public getSteps(): StepList<string[]> {
    return StepList.startStep(this.startRunTimeReviews).otherSteps(
      this.reviewDocument,
      this.collectReviews,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [reviewCompleted] };
  }
}

// WCP-15: an RPC adds instances. The join re-waits until intake closes and all report.

// The count is final once intake closes, so the join needs no lock.

const startedSampleCount = new Attribute("startedSampleCount", doubleCodec);
const isSampleIntakeClosed = new Attribute("isSampleIntakeClosed", booleanCodec);
const sampleAnalyzed = new Channel("sampleAnalyzed", stringCodec);
const sampleIntakeClosed = new Channel("sampleIntakeClosed", stringCodec);

class JoinSamples implements Step<string[]> {
  public getStepType(): string {
    return "JoinSamples";
  }

  public waitFor(context: Context, analyzedSamples: string[]): Wait {
    if (!isSampleIntakeClosed.get(context)) {
      return Wait.anyOf(sampleAnalyzed.forOne(), sampleIntakeClosed.forOne());
    }
    if (analyzedSamples.length === startedSampleCount.get(context)) {
      return Wait.skipImmediately();
    }
    return Wait.until(sampleAnalyzed.forOne());
  }

  public execute(context: Context, analyzedSamples: string[]): StepDecision {
    const updatedSamples = [...analyzedSamples, ...sampleAnalyzed.results(context)];
    const isEverySampleAnalyzed = updatedSamples.length === startedSampleCount.get(context);
    if (isSampleIntakeClosed.get(context) && isEverySampleAnalyzed) {
      return goTo(ReportSamples, updatedSamples);
    }
    return goTo(JoinSamples, updatedSamples);
  }
}

class AnalyzeSample implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "AnalyzeSample";
  }

  public execute(context: Context, sampleId: string): StepDecision {
    sampleAnalyzed.publish(context, sampleId);
    return deadEnd();
  }
}

class ReportSamples implements Step<string[]> {
  public getStepType(): string {
    return "ReportSamples";
  }

  public execute(_context: Context, analyzedSamples: string[]): StepDecision {
    return gracefulComplete([...analyzedSamples].sort());
  }
}

export class NoPriorKnowledgeMultipleInstancesFlow implements Flow<string[]> {
  private readonly joinSamples = new JoinSamples();
  private readonly analyzeSample = new AnalyzeSample();
  private readonly reportSamples = new ReportSamples();

  public getFlowType(): string {
    return "NoPriorKnowledgeMultipleInstancesFlow";
  }

  public getSteps(): StepList<string[]> {
    return StepList.startStep(this.joinSamples).otherSteps(this.analyzeSample, this.reportSamples);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [startedSampleCount, isSampleIntakeClosed],
      channels: [sampleAnalyzed, sampleIntakeClosed],
    };
  }

  @rpc({
    inputCodec: stringCodec,
    outputCodec: booleanCodec,
    lockAttributes: [startedSampleCount.lock()],
  })
  public addSample(context: Context, sampleId: string): RPCResult<boolean> {
    if (isSampleIntakeClosed.get(context)) {
      return { output: false };
    }
    startedSampleCount.set(context, startedSampleCount.get(context) + 1);
    return { output: true, nextSteps: [StepMovement.of(AnalyzeSample, sampleId)] };
  }

  @rpc({ lockAttributes: [startedSampleCount.lock()] })
  public closeSampleIntake(context: Context): void {
    isSampleIntakeClosed.set(context, true);
    sampleIntakeClosed.publish(context, "closed");
  }
}

// WCP-34 Static Partial Join for Multiple Instances: ForN(required) triggers the
// next Step. A reset Step consumes the remaining results.

export interface PartialInspection {
  readonly inspectionCount: number;
  readonly requiredInspections: number;
}

const inspectionReleased = new Channel("inspectionReleased", stringCodec);
const unitInspected = new Channel("unitInspected", doubleCodec);

class StartInspections implements Step<PartialInspection> {
  public getStepType(): string {
    return "StartInspections";
  }

  public execute(_context: Context, inspection: PartialInspection): StepDecision {
    return goToMany(
      ...Array.from({ length: inspection.inspectionCount }, (_, index) =>
        StepMovement.of(InspectUnit, index),
      ),
      StepMovement.of(AcceptInspections, inspection),
    );
  }
}

class InspectUnit implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "InspectUnit";
  }

  public waitFor(_context: Context, _unitIndex: number): Wait {
    return Wait.until(inspectionReleased.forOne());
  }

  public execute(context: Context, unitIndex: number): StepDecision {
    unitInspected.publish(context, unitIndex);
    return deadEnd();
  }
}

class AcceptInspections implements Step<PartialInspection> {
  public getStepType(): string {
    return "AcceptInspections";
  }

  public waitFor(_context: Context, inspection: PartialInspection): Wait {
    return Wait.until(unitInspected.forN(inspection.requiredInspections));
  }

  public execute(context: Context, inspection: PartialInspection): StepDecision {
    return goToMany(
      StepMovement.of(ApproveBatch, sortNumbers(unitInspected.results(context))),
      StepMovement.of(
        ResetInspectionJoin,
        inspection.inspectionCount - inspection.requiredInspections,
      ),
    );
  }
}

class ApproveBatch implements Step<number[]> {
  public getStepType(): string {
    return "ApproveBatch";
  }

  public execute(_context: Context, unitIndexes: number[]): StepDecision {
    return gracefulComplete(unitIndexes);
  }
}

class ResetInspectionJoin implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ResetInspectionJoin";
  }

  public waitFor(_context: Context, remainingInspections: number): Wait {
    return Wait.until(unitInspected.forN(remainingInspections));
  }

  public execute(context: Context, _remainingInspections: number): StepDecision {
    return gracefulComplete(sortNumbers(unitInspected.results(context)));
  }
}

export class StaticPartialJoinForMultipleInstancesFlow implements Flow<PartialInspection> {
  private readonly startInspections = new StartInspections();
  private readonly inspectUnit = new InspectUnit();
  private readonly acceptInspections = new AcceptInspections();
  private readonly approveBatch = new ApproveBatch();
  private readonly resetInspectionJoin = new ResetInspectionJoin();

  public getFlowType(): string {
    return "StaticPartialJoinForMultipleInstancesFlow";
  }

  public getSteps(): StepList<PartialInspection> {
    return StepList.startStep(this.startInspections).otherSteps(
      this.inspectUnit,
      this.acceptInspections,
      this.approveBatch,
      this.resetInspectionJoin,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [inspectionReleased, unitInspected] };
  }

  @rpc({ inputCodec: doubleCodec })
  public releaseInspections(context: Context, count: number): void {
    for (let index = 0; index < count; index++) {
      inspectionReleased.publish(context, "released");
    }
  }
}

// WCP-35 Cancelling Partial Join for Multiple Instances: the join cancels the
// instances that have not finished.

const quorumInspectionReleased = new Channel("quorumInspectionReleased", stringCodec);
const quorumUnitInspected = new Channel("quorumUnitInspected", doubleCodec);

class StartQuorumInspections implements Step<PartialInspection> {
  public getStepType(): string {
    return "StartQuorumInspections";
  }

  public execute(_context: Context, inspection: PartialInspection): StepDecision {
    return goToMany(
      ...Array.from({ length: inspection.inspectionCount }, (_, index) =>
        StepMovement.of(InspectQuorumUnit, index),
      ),
      StepMovement.of(AcceptQuorumInspections, inspection.requiredInspections),
    );
  }
}

class InspectQuorumUnit implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "InspectQuorumUnit";
  }

  public waitFor(_context: Context, _unitIndex: number): Wait {
    return Wait.until(quorumInspectionReleased.forOne());
  }

  public execute(context: Context, unitIndex: number): StepDecision {
    quorumUnitInspected.publish(context, unitIndex);
    return deadEnd();
  }
}

class AcceptQuorumInspections implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "AcceptQuorumInspections";
  }

  public waitFor(_context: Context, requiredInspections: number): Wait {
    return Wait.until(quorumUnitInspected.forN(requiredInspections));
  }

  public execute(context: Context, _requiredInspections: number): StepDecision {
    return withCancelingSteps(
      gracefulComplete(sortNumbers(quorumUnitInspected.results(context))),
      InspectQuorumUnit,
    );
  }
}

export class CancellingPartialJoinForMultipleInstancesFlow implements Flow<PartialInspection> {
  private readonly startQuorumInspections = new StartQuorumInspections();
  private readonly inspectQuorumUnit = new InspectQuorumUnit();
  private readonly acceptQuorumInspections = new AcceptQuorumInspections();

  public getFlowType(): string {
    return "CancellingPartialJoinForMultipleInstancesFlow";
  }

  public getSteps(): StepList<PartialInspection> {
    return StepList.startStep(this.startQuorumInspections).otherSteps(
      this.inspectQuorumUnit,
      this.acceptQuorumInspections,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [quorumInspectionReleased, quorumUnitInspected] };
  }

  @rpc({ inputCodec: doubleCodec })
  public releaseQuorumInspections(context: Context, count: number): void {
    for (let index = 0; index < count; index++) {
      quorumInspectionReleased.publish(context, "released");
    }
  }
}

// WCP-36: the join tests its condition after each bid, then closes admission.

// A shared lock stops late bids; a conflicting RPC fails fast and the caller retries.

export interface BidJoin {
  readonly requiredAcceptedBids: number;
  readonly acceptedBids: number[];
}

export interface EvaluatedBid {
  readonly amount: number;
  readonly isAccepted: boolean;
}

const MAXIMUM_ACCEPTED_BID_AMOUNT = 1000;
const isBidAdmissionClosed = new Attribute("isBidAdmissionClosed", booleanCodec);
const bidEvaluated = new Channel("bidEvaluated", jsonCodec<EvaluatedBid>({ typeName: "EvaluatedBid" }));

class JoinBids implements Step<BidJoin> {
  public getStepType(): string {
    return "JoinBids";
  }

  public getStepOptions(): StepOptions {
    return { executeLockAttributes: [isBidAdmissionClosed.lock()] };
  }

  public waitFor(_context: Context, _join: BidJoin): Wait {
    return Wait.until(bidEvaluated.forOne());
  }

  public execute(context: Context, join: BidJoin): StepDecision {
    const acceptedBids = [
      ...join.acceptedBids,
      ...bidEvaluated.results(context).filter((bid) => bid.isAccepted).map((bid) => bid.amount),
    ];
    if (acceptedBids.length >= join.requiredAcceptedBids) {
      isBidAdmissionClosed.set(context, true);
      return goTo(AwardContract, acceptedBids);
    }
    return goTo(JoinBids, { requiredAcceptedBids: join.requiredAcceptedBids, acceptedBids });
  }
}

class EvaluateBid implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "EvaluateBid";
  }

  public execute(context: Context, amount: number): StepDecision {
    bidEvaluated.publish(context, { amount, isAccepted: amount <= MAXIMUM_ACCEPTED_BID_AMOUNT });
    return deadEnd();
  }
}

class AwardContract implements Step<number[]> {
  public getStepType(): string {
    return "AwardContract";
  }

  public execute(_context: Context, acceptedBids: number[]): StepDecision {
    return gracefulComplete(sortNumbers(acceptedBids));
  }
}

export class DynamicPartialJoinForMultipleInstancesFlow implements Flow<BidJoin> {
  private readonly joinBids = new JoinBids();
  private readonly evaluateBid = new EvaluateBid();
  private readonly awardContract = new AwardContract();

  public getFlowType(): string {
    return "DynamicPartialJoinForMultipleInstancesFlow";
  }

  public getSteps(): StepList<BidJoin> {
    return StepList.startStep(this.joinBids).otherSteps(this.evaluateBid, this.awardContract);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [isBidAdmissionClosed], channels: [bidEvaluated] };
  }

  @rpc({
    inputCodec: doubleCodec,
    outputCodec: booleanCodec,
    lockAttributes: [isBidAdmissionClosed.lock()],
  })
  public addBid(context: Context, amount: number): RPCResult<boolean> {
    if (isBidAdmissionClosed.get(context)) {
      return { output: false };
    }
    return { output: true, nextSteps: [StepMovement.of(EvaluateBid, amount)] };
  }
}

export const multipleInstancesWithoutSynchronizationFlow =
  new MultipleInstancesWithoutSynchronizationFlow();
export const designTimeMultipleInstancesFlow = new DesignTimeMultipleInstancesFlow();
export const runTimeMultipleInstancesFlow = new RunTimeMultipleInstancesFlow();
export const noPriorKnowledgeMultipleInstancesFlow = new NoPriorKnowledgeMultipleInstancesFlow();
export const staticPartialJoinForMultipleInstancesFlow =
  new StaticPartialJoinForMultipleInstancesFlow();
export const cancellingPartialJoinForMultipleInstancesFlow =
  new CancellingPartialJoinForMultipleInstancesFlow();
export const dynamicPartialJoinForMultipleInstancesFlow =
  new DynamicPartialJoinForMultipleInstancesFlow();
