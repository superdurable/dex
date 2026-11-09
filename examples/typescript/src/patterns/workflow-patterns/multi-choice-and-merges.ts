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

// Workflow Patterns WCP-6, WCP-7, WCP-37 (multi-choice and synchronizing
// merges), WCP-8 (multi-merge), and WCP-38 (general synchronizing merge).

import {
  Channel,
  StepList,
  StepMovement,
  SubFlow,
  Wait,
  deadEnd,
  doubleCodec,
  goTo,
  goToMany,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepClass,
  type StepDecision,
} from "@superdurable/dex";

// WCP-6, WCP-7, and WCP-37: the choosing Step passes the branch count to the join.

export interface NotificationChoice {
  readonly sendEmail: boolean;
  readonly sendSms: boolean;
  readonly sendPush: boolean;
}

const notificationSent = new Channel("notificationSent", stringCodec);

class SelectNotificationChannels implements Step<NotificationChoice> {
  public getStepType(): string {
    return "SelectNotificationChannels";
  }

  public execute(_context: Context, choice: NotificationChoice): StepDecision {
    const selected: StepClass<void>[] = [];
    if (choice.sendEmail) {
      selected.push(SendEmail);
    }
    if (choice.sendSms) {
      selected.push(SendSms);
    }
    if (choice.sendPush) {
      selected.push(SendPush);
    }
    if (selected.length === 0) {
      return gracefulComplete([]);
    }
    return goToMany(
      StepMovement.of(SynchronizingMerge, selected.length),
      ...selected.map((branch) => StepMovement.of(branch, undefined)),
    );
  }
}

class SendEmail implements Step<void> {
  public getStepType(): string {
    return "SendEmail";
  }

  public execute(context: Context, _input: void): StepDecision {
    notificationSent.publish(context, "SendEmail");
    return deadEnd();
  }
}

class SendSms implements Step<void> {
  public getStepType(): string {
    return "SendSms";
  }

  public execute(context: Context, _input: void): StepDecision {
    notificationSent.publish(context, "SendSms");
    return deadEnd();
  }
}

class SendPush implements Step<void> {
  public getStepType(): string {
    return "SendPush";
  }

  public execute(context: Context, _input: void): StepDecision {
    notificationSent.publish(context, "SendPush");
    return deadEnd();
  }
}

class SynchronizingMerge implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "SynchronizingMerge";
  }

  public waitFor(_context: Context, selectedCount: number): Wait {
    return Wait.until(notificationSent.forN(selectedCount));
  }

  public execute(context: Context, _selectedCount: number): StepDecision {
    return gracefulComplete([...notificationSent.results(context)].sort());
  }
}

export class MultiChoiceSynchronizingMergeFlow implements Flow<NotificationChoice> {
  private readonly selectNotificationChannels = new SelectNotificationChannels();
  private readonly sendEmail = new SendEmail();
  private readonly sendSms = new SendSms();
  private readonly sendPush = new SendPush();
  private readonly synchronizingMerge = new SynchronizingMerge();

  public getFlowType(): string {
    return "MultiChoiceSynchronizingMergeFlow";
  }

  public getSteps(): StepList<NotificationChoice> {
    return StepList.startStep(this.selectNotificationChannels).otherSteps(
      this.sendEmail,
      this.sendSms,
      this.sendPush,
      this.synchronizingMerge,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [notificationSent] };
  }
}

// WCP-8 Multi-Merge: every arrival at RecordReview starts a new execution.

class StartReviews implements Step<void> {
  public getStepType(): string {
    return "StartReviews";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goToMany(
      StepMovement.of(LegalReview, undefined),
      StepMovement.of(FinanceReview, undefined),
    );
  }
}

class LegalReview implements Step<void> {
  public getStepType(): string {
    return "LegalReview";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(RecordReview, "LegalReview");
  }
}

class FinanceReview implements Step<void> {
  public getStepType(): string {
    return "FinanceReview";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(RecordReview, "FinanceReview");
  }
}

class RecordReview implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "RecordReview";
  }

  public execute(_context: Context, reviewer: string): StepDecision {
    return gracefulComplete(reviewer);
  }
}

export class MultiMergeFlow implements Flow<void> {
  private readonly startReviews = new StartReviews();
  private readonly legalReview = new LegalReview();
  private readonly financeReview = new FinanceReview();
  private readonly recordReview = new RecordReview();

  public getFlowType(): string {
    return "MultiMergeFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.startReviews).otherSteps(
      this.legalReview,
      this.financeReview,
      this.recordReview,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

// WCP-38: the region runs as a SubFlow, which completes once none of its Steps can run.

class DraftProposal implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "DraftProposal";
  }

  public execute(_context: Context, revision: number): StepDecision {
    return goToMany(
      StepMovement.of(ReviewProposal, revision),
      StepMovement.of(TranslateProposal, revision),
    );
  }
}

class ReviewProposal implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ReviewProposal";
  }

  public execute(_context: Context, revision: number): StepDecision {
    if (revision < 1) {
      return goTo(DraftProposal, revision + 1);
    }
    return gracefulComplete(`ReviewProposal-${revision}`);
  }
}

class TranslateProposal implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "TranslateProposal";
  }

  public execute(_context: Context, revision: number): StepDecision {
    return gracefulComplete(`TranslateProposal-${revision}`);
  }
}

export class ReviewRegionFlow implements Flow<number> {
  private readonly draftProposal = new DraftProposal();
  private readonly reviewProposal = new ReviewProposal();
  private readonly translateProposal = new TranslateProposal();

  public getFlowType(): string {
    return "ReviewRegionFlow";
  }

  public getSteps(): StepList<number> {
    return StepList.startStep(this.draftProposal).otherSteps(
      this.reviewProposal,
      this.translateProposal,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

class RunReviewRegion implements Step<void> {
  public constructor(private readonly reviewRegionFlow: ReviewRegionFlow) {}

  public getStepType(): string {
    return "RunReviewRegion";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(SubFlow.run(this.reviewRegionFlow, 0));
  }

  public execute(context: Context, _input: void): StepDecision {
    const regionOutputs = SubFlow.getConditionResults(context).completions.map((completion) =>
      completion.decode(stringCodec),
    );
    return goTo(PublishDecision, regionOutputs.sort());
  }
}

class PublishDecision implements Step<string[]> {
  public getStepType(): string {
    return "PublishDecision";
  }

  public execute(_context: Context, regionOutputs: string[]): StepDecision {
    return gracefulComplete(regionOutputs);
  }
}

export class GeneralSynchronizingMergeFlow implements Flow<void> {
  private readonly runReviewRegion: RunReviewRegion;
  private readonly publishDecision = new PublishDecision();

  public constructor(reviewRegionFlow: ReviewRegionFlow) {
    this.runReviewRegion = new RunReviewRegion(reviewRegionFlow);
  }

  public getFlowType(): string {
    return "GeneralSynchronizingMergeFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.runReviewRegion).otherSteps(this.publishDecision);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const multiChoiceSynchronizingMergeFlow = new MultiChoiceSynchronizingMergeFlow();
export const multiMergeFlow = new MultiMergeFlow();
export const reviewRegionFlow = new ReviewRegionFlow();
export const generalSynchronizingMergeFlow = new GeneralSynchronizingMergeFlow(reviewRegionFlow);
