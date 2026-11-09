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

// Cancellation Workflow Patterns: WCP-19 (cancel task), WCP-20 (cancel case),
// WCP-25 (cancel region), WCP-26 (cancel multiple instance activity), and
// WCP-27 (complete multiple instance activity).

import {
  Attribute,
  Channel,
  StepList,
  StepMovement,
  Wait,
  deadEnd,
  doubleCodec,
  goTo,
  goToMany,
  gracefulComplete,
  rpc,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

// WCP-19 Cancel Task: an RPC withdraws the waiting pickup Step and starts
// another Step in the same commit.

const courierArrived = new Channel("courierArrived", stringCodec);

class AwaitCourierPickup implements Step<void> {
  public getStepType(): string {
    return "AwaitCourierPickup";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(courierArrived.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("AwaitCourierPickup");
  }
}

class ArrangeSelfDelivery implements Step<void> {
  public getStepType(): string {
    return "ArrangeSelfDelivery";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("ArrangeSelfDelivery");
  }
}

export class CancelTaskFlow implements Flow<void> {
  private readonly awaitCourierPickup = new AwaitCourierPickup();
  private readonly arrangeSelfDelivery = new ArrangeSelfDelivery();

  public getFlowType(): string {
    return "CancelTaskFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.awaitCourierPickup).otherSteps(this.arrangeSelfDelivery);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [courierArrived] };
  }

  @rpc()
  public confirmCourierPickup(context: Context): void {
    courierArrived.publish(context, "arrived");
  }

  @rpc({ outputCodec: voidCodec })
  public cancelCourierPickup(_context: Context): RPCResult<void> {
    return {
      output: undefined,
      cancelingSteps: [AwaitCourierPickup],
      nextSteps: [StepMovement.of(ArrangeSelfDelivery, undefined)],
    };
  }
}

// WCP-20 Cancel Case: the Client stops the Flow. StopFlow cancels every active
// Step; a running SubFlow keeps running and must be stopped by its own ID.

const applicationReviewed = new Channel("applicationReviewed", stringCodec);

class ReviewApplication implements Step<void> {
  public getStepType(): string {
    return "ReviewApplication";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(applicationReviewed.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("ReviewApplication");
  }
}

export class CancelCaseFlow implements Flow<void> {
  private readonly reviewApplication = new ReviewApplication();

  public getFlowType(): string {
    return "CancelCaseFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.reviewApplication);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [applicationReviewed] };
  }
}

// WCP-25 Cancel Region: one RPC withdraws two Step types and leaves the
// sanctions screening running.

const identityVerified = new Channel("identityVerified", stringCodec);
const creditHistoryChecked = new Channel("creditHistoryChecked", stringCodec);
const sanctionsScreened = new Channel("sanctionsScreened", stringCodec);

class StartApplicationChecks implements Step<void> {
  public getStepType(): string {
    return "StartApplicationChecks";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goToMany(
      StepMovement.of(VerifyIdentity, undefined),
      StepMovement.of(CheckCreditHistory, undefined),
      StepMovement.of(ScreenSanctions, undefined),
    );
  }
}

class VerifyIdentity implements Step<void> {
  public getStepType(): string {
    return "VerifyIdentity";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(identityVerified.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("VerifyIdentity");
  }
}

class CheckCreditHistory implements Step<void> {
  public getStepType(): string {
    return "CheckCreditHistory";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(creditHistoryChecked.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("CheckCreditHistory");
  }
}

class ScreenSanctions implements Step<void> {
  public getStepType(): string {
    return "ScreenSanctions";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(sanctionsScreened.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("ScreenSanctions");
  }
}

class NotifyRejection implements Step<void> {
  public getStepType(): string {
    return "NotifyRejection";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("NotifyRejection");
  }
}

export class CancelRegionFlow implements Flow<void> {
  private readonly startApplicationChecks = new StartApplicationChecks();
  private readonly verifyIdentity = new VerifyIdentity();
  private readonly checkCreditHistory = new CheckCreditHistory();
  private readonly screenSanctions = new ScreenSanctions();
  private readonly notifyRejection = new NotifyRejection();

  public getFlowType(): string {
    return "CancelRegionFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.startApplicationChecks).otherSteps(
      this.verifyIdentity,
      this.checkCreditHistory,
      this.screenSanctions,
      this.notifyRejection,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [identityVerified, creditHistoryChecked, sanctionsScreened] };
  }

  @rpc({ outputCodec: voidCodec })
  public rejectApplication(_context: Context): RPCResult<void> {
    return {
      output: undefined,
      cancelingSteps: [VerifyIdentity, CheckCreditHistory],
      nextSteps: [StepMovement.of(NotifyRejection, undefined)],
    };
  }

  @rpc()
  public completeSanctionsScreening(context: Context): void {
    sanctionsScreened.publish(context, "screened");
  }
}

// WCP-26 Cancel Multiple Instance Activity: canceling the Step type withdraws
// the unfinished instances. Finished instances keep their results.

const finishedTranslationCount = new Attribute("finishedTranslationCount", doubleCodec);
const translationReleased = new Channel("translationReleased", stringCodec);

class StartTranslations implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "StartTranslations";
  }

  public execute(_context: Context, translationCount: number): StepDecision {
    return goToMany(
      ...Array.from({ length: translationCount }, (_, index) =>
        StepMovement.of(TranslateDocument, index),
      ),
    );
  }
}

class TranslateDocument implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "TranslateDocument";
  }

  public getStepOptions(): StepOptions {
    return { executeLockAttributes: [finishedTranslationCount.lock()] };
  }

  public waitFor(_context: Context, _documentIndex: number): Wait {
    return Wait.until(translationReleased.forOne());
  }

  public execute(context: Context, documentIndex: number): StepDecision {
    finishedTranslationCount.set(context, finishedTranslationCount.get(context) + 1);
    return gracefulComplete(documentIndex);
  }
}

class ReportTranslations implements Step<void> {
  public getStepType(): string {
    return "ReportTranslations";
  }

  public execute(context: Context, _input: void): StepDecision {
    return gracefulComplete(finishedTranslationCount.get(context));
  }
}

export class CancelMultipleInstanceTaskFlow implements Flow<number> {
  private readonly startTranslations = new StartTranslations();
  private readonly translateDocument = new TranslateDocument();
  private readonly reportTranslations = new ReportTranslations();

  public getFlowType(): string {
    return "CancelMultipleInstanceTaskFlow";
  }

  public getSteps(): StepList<number> {
    return StepList.startStep(this.startTranslations).otherSteps(
      this.translateDocument,
      this.reportTranslations,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [finishedTranslationCount], channels: [translationReleased] };
  }

  @rpc()
  public releaseTranslation(context: Context): void {
    translationReleased.publish(context, "released");
  }

  @rpc({ outputCodec: voidCodec })
  public cancelTranslations(_context: Context): RPCResult<void> {
    return {
      output: undefined,
      cancelingSteps: [TranslateDocument],
      nextSteps: [StepMovement.of(ReportTranslations, undefined)],
    };
  }
}

export { finishedTranslationCount };

// WCP-27 Complete Multiple Instance Activity: an RPC withdraws the remaining
// instances and the join, then passes the submitted reviews on.

const peerReviewReleased = new Channel("peerReviewReleased", stringCodec);
const peerReviewSubmitted = new Channel("peerReviewSubmitted", doubleCodec);

function sortReviewIndexes(indexes: readonly number[]): number[] {
  return [...indexes].sort((left, right) => left - right);
}

class StartPeerReviews implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "StartPeerReviews";
  }

  public execute(_context: Context, reviewerCount: number): StepDecision {
    return goToMany(
      ...Array.from({ length: reviewerCount }, (_, index) =>
        StepMovement.of(SubmitPeerReview, index),
      ),
      StepMovement.of(CollectPeerReviews, reviewerCount),
    );
  }
}

class SubmitPeerReview implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "SubmitPeerReview";
  }

  public waitFor(_context: Context, _reviewerIndex: number): Wait {
    return Wait.until(peerReviewReleased.forOne());
  }

  public execute(context: Context, reviewerIndex: number): StepDecision {
    peerReviewSubmitted.publish(context, reviewerIndex);
    return deadEnd();
  }
}

class CollectPeerReviews implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "CollectPeerReviews";
  }

  public waitFor(_context: Context, reviewerCount: number): Wait {
    return Wait.until(peerReviewSubmitted.forN(reviewerCount));
  }

  public execute(context: Context, _reviewerCount: number): StepDecision {
    return goTo(PublishReviewResults, sortReviewIndexes(peerReviewSubmitted.results(context)));
  }
}

class PublishReviewResults implements Step<number[]> {
  public getStepType(): string {
    return "PublishReviewResults";
  }

  public execute(_context: Context, reviewerIndexes: number[]): StepDecision {
    return gracefulComplete(reviewerIndexes);
  }
}

export class CompleteMultipleInstanceTaskFlow implements Flow<number> {
  private readonly startPeerReviews = new StartPeerReviews();
  private readonly submitPeerReview = new SubmitPeerReview();
  private readonly collectPeerReviews = new CollectPeerReviews();
  private readonly publishReviewResults = new PublishReviewResults();

  public getFlowType(): string {
    return "CompleteMultipleInstanceTaskFlow";
  }

  public getSteps(): StepList<number> {
    return StepList.startStep(this.startPeerReviews).otherSteps(
      this.submitPeerReview,
      this.collectPeerReviews,
      this.publishReviewResults,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [peerReviewReleased, peerReviewSubmitted] };
  }

  @rpc()
  public releasePeerReview(context: Context): void {
    peerReviewReleased.publish(context, "released");
  }

  @rpc({ loadChannels: [peerReviewSubmitted], outputCodec: doubleCodec })
  public getSubmittedPeerReviewCount(context: Context): RPCResult<number> {
    return { output: peerReviewSubmitted.size(context) };
  }

  @rpc({ loadChannels: [peerReviewSubmitted], outputCodec: voidCodec })
  public completePeerReviewsEarly(context: Context): RPCResult<void> {
    const submittedIndexes = peerReviewSubmitted
      .pendingMessages(context)
      .map((message) => message.value);
    return {
      output: undefined,
      cancelingSteps: [SubmitPeerReview, CollectPeerReviews],
      nextSteps: [StepMovement.of(PublishReviewResults, sortReviewIndexes(submittedIndexes))],
    };
  }
}

export const cancelTaskFlow = new CancelTaskFlow();
export const cancelCaseFlow = new CancelCaseFlow();
export const cancelRegionFlow = new CancelRegionFlow();
export const cancelMultipleInstanceTaskFlow = new CancelMultipleInstanceTaskFlow();
export const completeMultipleInstanceTaskFlow = new CompleteMultipleInstanceTaskFlow();
