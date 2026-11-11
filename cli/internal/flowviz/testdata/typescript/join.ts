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
    if (reviewers.length > 2) {
      const branchBuilders = reviewers.map((reviewer) => (round: JoinRound) => StepMovement.of(ReviewDocument, {reviewer, round}));
      return goToMany(...reviewCompleted.fork(context, branchBuilders, (round) => StepMovement.of(CollectReviews, round)));
    }
    if (reviewers.length > 1) {
      // A single push adds one branch.
      const pairBuilders = [(round: JoinRound) => StepMovement.of(ReviewDocument, {reviewer: "lead", round})];
      pairBuilders.push((round: JoinRound) => StepMovement.of(EscalateReview, {reviewer: "backup", round}));
      return goToMany(...reviewCompleted.fork(context, pairBuilders, (round) => StepMovement.of(CollectReviews, round)));
    }
    if (reviewers.includes("loop")) {
      // A push in a loop adds repeated branches.
      const loopBuilders: ((round: JoinRound) => StepMovement<ReviewInput>)[] = [];
      for (const reviewer of reviewers) {
        loopBuilders.push((round: JoinRound) => StepMovement.of(EscalateReview, {reviewer, round}));
      }
      return goToMany(...reviewCompleted.fork(context, loopBuilders, (round) => StepMovement.of(CollectReviews, round)));
    }
    if (reviewers.includes("late")) {
      // A push or another use after the fork does not change its branches.
      const lateBuilders = [(round: JoinRound) => StepMovement.of(ReviewDocument, {reviewer: "early", round})];
      const lateMovements = reviewCompleted.fork(context, lateBuilders, (round) => StepMovement.of(CollectReviews, round));
      lateBuilders.push((round: JoinRound) => StepMovement.of(EscalateReview, {reviewer: "late", round}));
      lateBuilders.reverse();
      return goToMany(...lateMovements);
    }
    if (reviewers.includes("each")) {
      // A push in a forEach callback adds repeated branches.
      const eachBuilders: ((round: JoinRound) => StepMovement<ReviewInput>)[] = [];
      reviewers.forEach((reviewer) => eachBuilders.push((round: JoinRound) => StepMovement.of(EscalateReview, {reviewer, round})));
      return goToMany(...reviewCompleted.fork(context, eachBuilders, (round) => StepMovement.of(CollectReviews, round)));
    }
    if (reviewers.includes("plain")) {
      // A single push onto a plain movement list adds one movement.
      const plainMovements = [StepMovement.of(ReviewDocument, {reviewer: "plain", round: {roundKey: "plain", branchCount: 2}})];
      plainMovements.push(StepMovement.of(EscalateReview, {reviewer: "plain", round: {roundKey: "plain", branchCount: 2}}));
      return goToMany(...plainMovements);
    }
    if (reviewers.includes("sibling")) {
      // A push in the other branch of an if never reaches this fork.
      const siblingBuilders = [(round: JoinRound) => StepMovement.of(ReviewDocument, {reviewer: "main", round})];
      if (reviewers.length > 4) {
        siblingBuilders.push((round: JoinRound) => StepMovement.of(EscalateReview, {reviewer: "sibling", round}));
        return gracefulComplete([]);
      } else {
        return goToMany(...reviewCompleted.fork(context, siblingBuilders, (round) => StepMovement.of(CollectReviews, round)));
      }
    }
    if (reviewers.includes("shadow")) {
      // A push on a shadowing inner variable leaves the outer list unchanged.
      const shadowBuilders = [(round: JoinRound) => StepMovement.of(ReviewDocument, {reviewer: "outer", round})];
      {
        const shadowBuilders: ((round: JoinRound) => StepMovement<ReviewInput>)[] = [];
        shadowBuilders.push((round: JoinRound) => StepMovement.of(EscalateReview, {reviewer: "inner", round}));
      }
      return goToMany(...reviewCompleted.fork(context, shadowBuilders, (round) => StepMovement.of(CollectReviews, round)));
    }
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
// dex:explanation text:"Report an escalated review result and end the branch."
class EscalateReview implements Step<ReviewInput> {
  getStepType(): string {
    return "EscalateReview";
  }

  execute(context: Context, input: ReviewInput): StepDecision {
    return reviewCompleted.endBranch(context, input.round, `escalated: ${input.reviewer}`);
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
    return StepList.startStep(new RequestReviews()).otherSteps(new ReviewDocument(), new EscalateReview(), new CollectReviews());
  }

  getPersistenceSchema(): PersistenceSchema {
    return {channels: [reviewCompleted]};
  }
}
