// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {Join, StepList, StepMovement, goToMany, gracefulComplete, joinRoundCodec, stringCodec, type Context, type Flow, type JoinRound, type PersistenceSchema, type Step, type StepDecision} from "@superdurable/dex";

const reviewCompleted = new Join("reviewCompleted", stringCodec);

interface ReviewInput {
  readonly reviewer: string;
  readonly round: JoinRound;
}

// dex:group group-id:join group-label:"Join"
// dex:explanation text:"Start one review per reviewer, or a fixed pair of reviews, and the join Step."
class RequestReviews implements Step<readonly string[]> {
  getStepType(): string {
    return "RequestReviews";
  }

  execute(context: Context, reviewers: readonly string[]): StepDecision {
    if (reviewers.length > 0) {
      return goToMany(...reviewCompleted.fork(
        context,
        reviewers.map((reviewer) => (round: JoinRound) => StepMovement.of(ReviewDocument, {reviewer, round})),
        (round) => StepMovement.of(CollectReviews, round),
      ));
    }
    const movements = reviewCompleted.forkWithRoundKey(
      "fixed-reviewers",
      [
        (round) => StepMovement.of(ReviewDocument, {reviewer: "legal", round}),
        (round) => StepMovement.of(ReviewDocument, {reviewer: "finance", round}),
      ],
      (round) => StepMovement.of(CollectReviews, round),
    );
    return goToMany(...movements);
  }
}

// dex:group group-id:join group-label:"Join"
// dex:explanation text:"Report one review result and end the branch."
class ReviewDocument implements Step<ReviewInput> {
  getStepType(): string {
    return "ReviewDocument";
  }

  execute(context: Context, input: ReviewInput): StepDecision {
    return reviewCompleted.endBranch(context, input.round, input.reviewer);
  }
}

// dex:group group-id:join group-label:"Join"
// dex:explanation text:"Complete the Flow with the collected review results."
class CollectReviews implements Step<JoinRound> {
  readonly inputCodec = joinRoundCodec;

  getStepType(): string {
    return "CollectReviews";
  }

  execute(context: Context, round: JoinRound): StepDecision {
    return gracefulComplete(reviewCompleted.branchResults(context, round));
  }
}

export class ReviewJoinFlow implements Flow<readonly string[]> {
  getFlowType(): string {
    return "ReviewJoinFlow";
  }

  getSteps() {
    return StepList.startStep(new RequestReviews()).otherSteps(new ReviewDocument(), new CollectReviews());
  }

  getPersistenceSchema(): PersistenceSchema {
    return {channels: [reviewCompleted]};
  }
}
