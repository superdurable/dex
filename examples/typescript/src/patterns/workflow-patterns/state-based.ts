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

// State-based Workflow Patterns: WCP-16 (deferred choice), WCP-17 (interleaved
// parallel routing), WCP-18 (milestone), WCP-39 (critical section), and
// WCP-40 (interleaved routing).

import { setTimeout as sleep } from "node:timers/promises";

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
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

/** When one task ran, in Unix epoch milliseconds. */
export interface TaskInterval {
  readonly task: string;
  readonly startedAtMillis: number;
  readonly finishedAtMillis: number;
}

const taskIntervalCodec = jsonCodec<TaskInterval>({ typeName: "TaskInterval" });
const INTERLEAVED_TASK_MILLIS = 200;
const CRITICAL_SECTION_STEP_MILLIS = 100;

async function runTimedTask(task: string, durationMillis: number): Promise<TaskInterval> {
  const startedAtMillis = Date.now();
  await sleep(durationMillis);
  return { task, startedAtMillis, finishedAtMillis: Date.now() };
}

// WCP-16 Deferred Choice: AnyOf waits for whichever response comes first.
// Dex consumes only the selected Channel condition.

const offerPresented = new Channel("offerPresented", stringCodec);
const offerAccepted = new Channel("offerAccepted", stringCodec);
const offerDeclined = new Channel("offerDeclined", stringCodec);

class PresentOffer implements Step<void> {
  public getStepType(): string {
    return "PresentOffer";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(offerPresented.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(AwaitCustomerResponse, undefined);
  }
}

class AwaitCustomerResponse implements Step<void> {
  public getStepType(): string {
    return "AwaitCustomerResponse";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(offerAccepted.forOne(), offerDeclined.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    if (offerAccepted.results(context).length > 0) {
      return goTo(FulfillOffer, undefined);
    }
    return goTo(ArchiveOffer, undefined);
  }
}

class FulfillOffer implements Step<void> {
  public getStepType(): string {
    return "FulfillOffer";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("FulfillOffer");
  }
}

class ArchiveOffer implements Step<void> {
  public getStepType(): string {
    return "ArchiveOffer";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("ArchiveOffer");
  }
}

export class DeferredChoiceFlow implements Flow<void> {
  private readonly presentOfferStep = new PresentOffer();
  private readonly awaitCustomerResponse = new AwaitCustomerResponse();
  private readonly fulfillOffer = new FulfillOffer();
  private readonly archiveOffer = new ArchiveOffer();

  public getFlowType(): string {
    return "DeferredChoiceFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.presentOfferStep).otherSteps(
      this.awaitCustomerResponse,
      this.fulfillOffer,
      this.archiveOffer,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [offerPresented, offerAccepted, offerDeclined] };
  }

  @rpc()
  public presentOffer(context: Context): void {
    offerPresented.publish(context, "presented");
  }

  @rpc()
  public acceptOffer(context: Context): void {
    offerAccepted.publish(context, "accepted");
  }

  @rpc()
  public declineOffer(context: Context): void {
    offerDeclined.publish(context, "declined");
  }
}

// WCP-17: one shared Execute lock keeps the tasks apart; GoTo orders two of them.

// The lock covers one Execute, not a WaitFor or a task that spans Steps.

const onboardingTaskLock = new Attribute("onboardingTaskLock", booleanCodec);
const onboardingTaskFinished = new Channel("onboardingTaskFinished", taskIntervalCodec);
const ONBOARDING_TASK_COUNT = 3;
const onboardingTaskOptions: StepOptions = {
  executeLockAttributes: [onboardingTaskLock.lock()],
};

class StartOnboardingTasks implements Step<void> {
  public getStepType(): string {
    return "StartOnboardingTasks";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goToMany(
      StepMovement.of(PrepareContract, undefined),
      StepMovement.of(RunCreditCheck, undefined),
      StepMovement.of(CompleteOnboardingTasks, ONBOARDING_TASK_COUNT),
    );
  }
}

class PrepareContract implements Step<void> {
  public getStepType(): string {
    return "PrepareContract";
  }

  public getStepOptions(): StepOptions {
    return onboardingTaskOptions;
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    onboardingTaskFinished.publish(context, await runTimedTask("PrepareContract", INTERLEAVED_TASK_MILLIS));
    return goTo(SignContract, undefined);
  }
}

class SignContract implements Step<void> {
  public getStepType(): string {
    return "SignContract";
  }

  public getStepOptions(): StepOptions {
    return onboardingTaskOptions;
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    onboardingTaskFinished.publish(context, await runTimedTask("SignContract", INTERLEAVED_TASK_MILLIS));
    return deadEnd();
  }
}

class RunCreditCheck implements Step<void> {
  public getStepType(): string {
    return "RunCreditCheck";
  }

  public getStepOptions(): StepOptions {
    return onboardingTaskOptions;
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    onboardingTaskFinished.publish(context, await runTimedTask("RunCreditCheck", INTERLEAVED_TASK_MILLIS));
    return deadEnd();
  }
}

class CompleteOnboardingTasks implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "CompleteOnboardingTasks";
  }

  public waitFor(_context: Context, taskCount: number): Wait {
    return Wait.until(onboardingTaskFinished.forN(taskCount));
  }

  public execute(context: Context, _taskCount: number): StepDecision {
    return gracefulComplete(onboardingTaskFinished.results(context));
  }
}

export class InterleavedParallelRoutingFlow implements Flow<void> {
  private readonly startOnboardingTasks = new StartOnboardingTasks();
  private readonly prepareContract = new PrepareContract();
  private readonly signContract = new SignContract();
  private readonly runCreditCheck = new RunCreditCheck();
  private readonly completeOnboardingTasks = new CompleteOnboardingTasks();

  public getFlowType(): string {
    return "InterleavedParallelRoutingFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.startOnboardingTasks).otherSteps(
      this.prepareContract,
      this.signContract,
      this.runCreditCheck,
      this.completeOnboardingTasks,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [onboardingTaskLock], channels: [onboardingTaskFinished] };
  }
}

// WCP-18 Milestone: the RPC starts ApplyConfigChange only while the window is open.

const isChangeWindowOpen = new Attribute("isChangeWindowOpen", booleanCodec);
const releaseApproved = new Channel("releaseApproved", stringCodec);
const releaseFinalized = new Channel("releaseFinalized", stringCodec);

class OpenChangeWindow implements Step<void> {
  public getStepType(): string {
    return "OpenChangeWindow";
  }

  public execute(context: Context, _input: void): StepDecision {
    isChangeWindowOpen.set(context, true);
    return goTo(AwaitReleaseApproval, undefined);
  }
}

class AwaitReleaseApproval implements Step<void> {
  public getStepType(): string {
    return "AwaitReleaseApproval";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(releaseApproved.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    isChangeWindowOpen.set(context, false);
    return goTo(AwaitReleaseFinalization, undefined);
  }
}

class AwaitReleaseFinalization implements Step<void> {
  public getStepType(): string {
    return "AwaitReleaseFinalization";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(releaseFinalized.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("AwaitReleaseFinalization");
  }
}

class ApplyConfigChange implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "ApplyConfigChange";
  }

  public execute(_context: Context, change: string): StepDecision {
    return gracefulComplete(change);
  }
}

export class MilestoneFlow implements Flow<void> {
  private readonly openChangeWindow = new OpenChangeWindow();
  private readonly awaitReleaseApproval = new AwaitReleaseApproval();
  private readonly awaitReleaseFinalization = new AwaitReleaseFinalization();
  private readonly applyConfigChange = new ApplyConfigChange();

  public getFlowType(): string {
    return "MilestoneFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.openChangeWindow).otherSteps(
      this.awaitReleaseApproval,
      this.awaitReleaseFinalization,
      this.applyConfigChange,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [isChangeWindowOpen], channels: [releaseApproved, releaseFinalized] };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: booleanCodec })
  public requestConfigChange(context: Context, change: string): RPCResult<boolean> {
    if (!isChangeWindowOpen.get(context)) {
      return { output: false };
    }
    return { output: true, nextSteps: [StepMovement.of(ApplyConfigChange, change)] };
  }

  @rpc()
  public approveRelease(context: Context): void {
    releaseApproved.publish(context, "approved");
  }

  @rpc()
  public finalizeRelease(context: Context): void {
    releaseFinalized.publish(context, "finalized");
  }
}

export { isChangeWindowOpen };

// WCP-39: a lock covers one Step method, so a two-Step section holds a Channel token.

const criticalSectionToken = new Channel("criticalSectionToken", stringCodec);
const criticalSectionFinished = new Channel("criticalSectionFinished", taskIntervalCodec);
const CRITICAL_SECTION_COUNT = 2;

class StartCriticalSections implements Step<void> {
  public getStepType(): string {
    return "StartCriticalSections";
  }

  public execute(context: Context, _input: void): StepDecision {
    criticalSectionToken.publish(context, "token");
    return goToMany(
      StepMovement.of(ReserveStock, undefined),
      StepMovement.of(RecalculatePrice, undefined),
      StepMovement.of(FinishCriticalSections, CRITICAL_SECTION_COUNT),
    );
  }
}

class ReserveStock implements Step<void> {
  public getStepType(): string {
    return "ReserveStock";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(criticalSectionToken.forOne());
  }

  public async execute(_context: Context, _input: void): Promise<StepDecision> {
    const sectionStartedAtMillis = Date.now();
    await sleep(CRITICAL_SECTION_STEP_MILLIS);
    return goTo(CommitStock, sectionStartedAtMillis);
  }
}

class CommitStock implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "CommitStock";
  }

  public async execute(context: Context, sectionStartedAtMillis: number): Promise<StepDecision> {
    await sleep(CRITICAL_SECTION_STEP_MILLIS);
    criticalSectionFinished.publish(context, {
      task: "inventory",
      startedAtMillis: sectionStartedAtMillis,
      finishedAtMillis: Date.now(),
    });
    criticalSectionToken.publish(context, "token");
    return deadEnd();
  }
}

class RecalculatePrice implements Step<void> {
  public getStepType(): string {
    return "RecalculatePrice";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(criticalSectionToken.forOne());
  }

  public async execute(_context: Context, _input: void): Promise<StepDecision> {
    const sectionStartedAtMillis = Date.now();
    await sleep(CRITICAL_SECTION_STEP_MILLIS);
    return goTo(PublishPrice, sectionStartedAtMillis);
  }
}

class PublishPrice implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "PublishPrice";
  }

  public async execute(context: Context, sectionStartedAtMillis: number): Promise<StepDecision> {
    await sleep(CRITICAL_SECTION_STEP_MILLIS);
    criticalSectionFinished.publish(context, {
      task: "pricing",
      startedAtMillis: sectionStartedAtMillis,
      finishedAtMillis: Date.now(),
    });
    criticalSectionToken.publish(context, "token");
    return deadEnd();
  }
}

class FinishCriticalSections implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "FinishCriticalSections";
  }

  public waitFor(_context: Context, sectionCount: number): Wait {
    return Wait.until(criticalSectionFinished.forN(sectionCount));
  }

  public execute(context: Context, _sectionCount: number): StepDecision {
    return gracefulComplete(criticalSectionFinished.results(context));
  }
}

export class CriticalSectionFlow implements Flow<void> {
  private readonly startCriticalSections = new StartCriticalSections();
  private readonly reserveStock = new ReserveStock();
  private readonly commitStock = new CommitStock();
  private readonly recalculatePrice = new RecalculatePrice();
  private readonly publishPrice = new PublishPrice();
  private readonly finishCriticalSections = new FinishCriticalSections();

  public getFlowType(): string {
    return "CriticalSectionFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.startCriticalSections).otherSteps(
      this.reserveStock,
      this.commitStock,
      this.recalculatePrice,
      this.publishPrice,
      this.finishCriticalSections,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [criticalSectionToken, criticalSectionFinished] };
  }
}

// WCP-40 (+/-): each audit runs once, in any order, one at a time.

// The lock covers one Execute, not a WaitFor or a task that spans Steps.

const auditLock = new Attribute("auditLock", booleanCodec);
const auditFinished = new Channel("auditFinished", taskIntervalCodec);
const AUDIT_COUNT = 3;
const auditOptions: StepOptions = { executeLockAttributes: [auditLock.lock()] };

class StartAudits implements Step<void> {
  public getStepType(): string {
    return "StartAudits";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goToMany(
      StepMovement.of(AuditPayroll, undefined),
      StepMovement.of(AuditExpenses, undefined),
      StepMovement.of(AuditTaxes, undefined),
      StepMovement.of(CompleteAudits, AUDIT_COUNT),
    );
  }
}

class AuditPayroll implements Step<void> {
  public getStepType(): string {
    return "AuditPayroll";
  }

  public getStepOptions(): StepOptions {
    return auditOptions;
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    auditFinished.publish(context, await runTimedTask("AuditPayroll", INTERLEAVED_TASK_MILLIS));
    return deadEnd();
  }
}

class AuditExpenses implements Step<void> {
  public getStepType(): string {
    return "AuditExpenses";
  }

  public getStepOptions(): StepOptions {
    return auditOptions;
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    auditFinished.publish(context, await runTimedTask("AuditExpenses", INTERLEAVED_TASK_MILLIS));
    return deadEnd();
  }
}

class AuditTaxes implements Step<void> {
  public getStepType(): string {
    return "AuditTaxes";
  }

  public getStepOptions(): StepOptions {
    return auditOptions;
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    auditFinished.publish(context, await runTimedTask("AuditTaxes", INTERLEAVED_TASK_MILLIS));
    return deadEnd();
  }
}

class CompleteAudits implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "CompleteAudits";
  }

  public waitFor(_context: Context, auditCount: number): Wait {
    return Wait.until(auditFinished.forN(auditCount));
  }

  public execute(context: Context, _auditCount: number): StepDecision {
    return gracefulComplete(auditFinished.results(context));
  }
}

export class InterleavedRoutingFlow implements Flow<void> {
  private readonly startAudits = new StartAudits();
  private readonly auditPayroll = new AuditPayroll();
  private readonly auditExpenses = new AuditExpenses();
  private readonly auditTaxes = new AuditTaxes();
  private readonly completeAudits = new CompleteAudits();

  public getFlowType(): string {
    return "InterleavedRoutingFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.startAudits).otherSteps(
      this.auditPayroll,
      this.auditExpenses,
      this.auditTaxes,
      this.completeAudits,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [auditLock], channels: [auditFinished] };
  }
}

export const deferredChoiceFlow = new DeferredChoiceFlow();
export const interleavedParallelRoutingFlow = new InterleavedParallelRoutingFlow();
export const milestoneFlow = new MilestoneFlow();
export const criticalSectionFlow = new CriticalSectionFlow();
export const interleavedRoutingFlow = new InterleavedRoutingFlow();
