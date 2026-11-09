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
  jsonCodec,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepClass,
  type StepDecision,
  type StepOptions,
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

// WCP-38: an Attribute counts the threads that can still reach PublishDocument.

// The commit that brings the count to 0 publishes the sentinel the merge waits for.

export interface DocumentReviewPlan {
  readonly isLegalCheckNeeded: boolean;
  readonly isFinanceCheckNeeded: boolean;
  readonly financeReworkRounds: number;
  readonly isFinanceArchived: boolean;
}

export interface DocumentDraft {
  readonly plan: DocumentReviewPlan;
  readonly revision: number;
}

const liveBranchCount = new Attribute("liveBranchCount", doubleCodec);
const mergedReviews = new Attribute("mergedReviews", jsonCodec<string[]>({ typeName: "Reviews" }));
const mergeSentinel = new Channel("mergeSentinel", stringCodec);
const liveBranchCountLock: StepOptions = { executeLockAttributes: [liveBranchCount.lock()] };

// Call only from a Step that holds the liveBranchCount lock.
function changeLiveBranchCount(context: Context, delta: number): void {
  const count = liveBranchCount.get(context) + delta;
  liveBranchCount.set(context, count);
  if (count === 0) {
    mergeSentinel.publish(context, "done");
  }
}

function recordArrival(context: Context, review: string): void {
  mergedReviews.set(context, [...(mergedReviews.get(context) ?? []), review]);
  changeLiveBranchCount(context, -1);
}

class StartDocumentReview implements Step<DocumentReviewPlan> {
  public getStepType(): string {
    return "StartDocumentReview";
  }

  public getStepOptions(): StepOptions {
    return liveBranchCountLock;
  }

  public execute(context: Context, plan: DocumentReviewPlan): StepDecision {
    liveBranchCount.set(context, 1);
    return goToMany(
      StepMovement.of(DraftDocument, { plan, revision: 0 }),
      StepMovement.of(PublishDocument, undefined),
    );
  }
}

class DraftDocument implements Step<DocumentDraft> {
  public getStepType(): string {
    return "DraftDocument";
  }

  public getStepOptions(): StepOptions {
    return liveBranchCountLock;
  }

  public execute(context: Context, draft: DocumentDraft): StepDecision {
    const isRework = draft.revision > 0;
    const movements: StepMovement<any>[] = [];
    if (isRework || draft.plan.isLegalCheckNeeded) {
      movements.push(StepMovement.of(CheckLegalTerms, draft.revision));
    }
    if (isRework || draft.plan.isFinanceCheckNeeded) {
      movements.push(StepMovement.of(CheckFinanceTerms, draft));
    }
    changeLiveBranchCount(context, movements.length - 1);
    return movements.length === 0 ? deadEnd() : goToMany(...movements);
  }
}

class CheckLegalTerms implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "CheckLegalTerms";
  }

  public getStepOptions(): StepOptions {
    return liveBranchCountLock;
  }

  public execute(context: Context, revision: number): StepDecision {
    recordArrival(context, `CheckLegalTerms-${revision}`);
    return deadEnd();
  }
}

class CheckFinanceTerms implements Step<DocumentDraft> {
  public getStepType(): string {
    return "CheckFinanceTerms";
  }

  public getStepOptions(): StepOptions {
    return liveBranchCountLock;
  }

  public execute(context: Context, draft: DocumentDraft): StepDecision {
    if (draft.revision < draft.plan.financeReworkRounds) {
      return goTo(DraftDocument, { plan: draft.plan, revision: draft.revision + 1 });
    }
    if (draft.plan.isFinanceArchived) {
      changeLiveBranchCount(context, -1);
      return goTo(ArchiveDocument, undefined);
    }
    recordArrival(context, `CheckFinanceTerms-${draft.revision}`);
    return deadEnd();
  }
}

class ArchiveDocument implements Step<void> {
  public getStepType(): string {
    return "ArchiveDocument";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("ArchiveDocument");
  }
}

class PublishDocument implements Step<void> {
  public getStepType(): string {
    return "PublishDocument";
  }

  public getStepOptions(): StepOptions {
    return liveBranchCountLock;
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(mergeSentinel.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    return gracefulComplete([...(mergedReviews.get(context) ?? [])].sort());
  }
}

export class GeneralSynchronizingMergeFlow implements Flow<DocumentReviewPlan> {
  private readonly startDocumentReview = new StartDocumentReview();
  private readonly draftDocument = new DraftDocument();
  private readonly checkLegalTerms = new CheckLegalTerms();
  private readonly checkFinanceTerms = new CheckFinanceTerms();
  private readonly archiveDocument = new ArchiveDocument();
  private readonly publishDocument = new PublishDocument();

  public getFlowType(): string {
    return "GeneralSynchronizingMergeFlow";
  }

  public getSteps(): StepList<DocumentReviewPlan> {
    return StepList.startStep(this.startDocumentReview).otherSteps(
      this.draftDocument,
      this.checkLegalTerms,
      this.checkFinanceTerms,
      this.archiveDocument,
      this.publishDocument,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [liveBranchCount, mergedReviews], channels: [mergeSentinel] };
  }
}

export const multiChoiceSynchronizingMergeFlow = new MultiChoiceSynchronizingMergeFlow();
export const multiMergeFlow = new MultiMergeFlow();
export const generalSynchronizingMergeFlow = new GeneralSynchronizingMergeFlow();
