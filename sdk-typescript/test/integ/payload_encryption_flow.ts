// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  Action,
  Attribute,
  AttributeMap,
  AttributeMatch,
  Channel,
  ExecuteFailure,
  IndexType,
  StepList,
  Stream,
  SubFlow,
  Wait,
  forceFail,
  goTo,
  gracefulComplete,
  int64Codec,
  jsonCodec,
  rpc,
  stringCodec,
  whenAttributeMatches,
  type AsyncContext,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

export const ENCRYPTED_SEARCH_KEY = "EncryptedSearchStatus";

export interface RoundTripInput {
  readonly text: string;
  readonly searchValue: string;
  readonly failChild: boolean;
}

interface CollectedInput {
  readonly text: string;
  readonly collected: string;
  readonly failChild: boolean;
}

export interface RoundTripOutput {
  readonly collected: string;
  readonly childFlowId: string;
  readonly childStatus: string;
  readonly childError: string | undefined;
  readonly childOutput: ChildOutput | undefined;
}

interface ChildInput {
  readonly text: string;
}

export interface ChildOutput {
  readonly echo: string;
  readonly childFlowId: string;
  readonly childNote: string;
}

const roundTripInputCodec = jsonCodec<RoundTripInput>();
const collectedInputCodec = jsonCodec<CollectedInput>();
const childInputCodec = jsonCodec<ChildInput>();
export const childOutputCodec = jsonCodec<ChildOutput>();
export const roundTripOutputCodec = jsonCodec<RoundTripOutput>();
export const noteCodec = jsonCodec<{ readonly text: string }>();

export const LARGE_NOTE = "large encrypted note ".repeat(20);

class EncryptedChildStep implements Step<ChildInput> {
  public readonly inputCodec = childInputCodec;

  public constructor(private readonly flow: EncryptedChildFlow) {}

  public getStepType(): string {
    return "EncryptedChildStep";
  }

  public execute(context: Context, input: ChildInput): StepDecision {
    this.flow.childNote.set(context, `child-note-${input.text}`);
    if (input.text === "fail") {
      return forceFail("child declined card 4242");
    }
    return gracefulComplete({ echo: input.text, childFlowId: context.flowId, childNote: this.flow.childNote.get(context) });
  }
}

export class EncryptedChildFlow implements Flow<ChildInput> {
  public readonly childNote = new Attribute("encrypted-child-note", stringCodec);
  private readonly start = new EncryptedChildStep(this);

  public getFlowType(): string {
    return "EncryptedChildFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.childNote] };
  }
}

class EncryptedSubFlowStep implements Step<CollectedInput> {
  public readonly inputCodec = collectedInputCodec;

  public constructor(private readonly child: EncryptedChildFlow) {}

  public getStepType(): string {
    return "EncryptedSubFlowStep";
  }

  public waitFor(_context: Context, input: CollectedInput): Wait {
    return Wait.until(SubFlow.run(this.child, { text: input.failChild ? "fail" : input.text }));
  }

  public execute(context: Context, input: CollectedInput): StepDecision {
    const result = SubFlow.getConditionResults(context);
    return gracefulComplete({
      collected: input.collected,
      childFlowId: SubFlow.getFlowId(context),
      childStatus: result.status,
      childError: result.errorMessage,
      childOutput: result.status === "completed" ? result.singleOutput(childOutputCodec) : undefined,
    });
  }
}

class EncryptedStartStep implements Step<RoundTripInput> {
  public readonly inputCodec = roundTripInputCodec;

  public constructor(
    private readonly flow: EncryptedRoundTripFlow,
    private readonly next: EncryptedSubFlowStep,
  ) {}

  public getStepType(): string {
    return "EncryptedStartStep";
  }

  public async waitFor(context: AsyncContext, input: RoundTripInput): Promise<Wait> {
    this.flow.secretNote.set(context, `secret-${input.text}`);
    this.flow.secretCount.set(context, 42n);
    this.flow.largeNote.set(context, LARGE_NOTE);
    this.flow.searchStatus.set(context, input.searchValue);
    this.flow.stage.set(context, "waiting");
    this.flow.notes.set(context, "first", { text: `map-${input.text}` });
    context.recordEvent("encrypted-event", `event-${input.text}`, stringCodec);
    context.setStepExecutionLocal("encrypted-local", `local-${input.text}`, stringCodec);
    this.flow.progress.write(context, `progress-${input.text}`);
    return Wait.until(this.flow.messages.forOne());
  }

  public execute(context: Context, input: RoundTripInput): StepDecision {
    const [message] = this.flow.messages.results(context);
    const local = context.getStepExecutionLocal("encrypted-local", stringCodec);
    const collected = [
      message,
      local,
      this.flow.secretNote.get(context),
      this.flow.largeNote.get(context).length,
      this.flow.secretCount.get(context),
    ].join("|");
    return goTo(this.next, { text: input.text, collected, failChild: input.failChild });
  }

  public getStepOptions(): StepOptions {
    return { heartbeatTimeoutMs: 10_000 };
  }
}

export class EncryptedRoundTripFlow implements Flow<RoundTripInput> {
  public readonly secretNote = new Attribute("encrypted-secret-note", stringCodec);
  public readonly secretCount = new Attribute("encrypted-secret-count", int64Codec);
  public readonly largeNote = new Attribute("encrypted-large-note", stringCodec);
  public readonly searchStatus = new Attribute(ENCRYPTED_SEARCH_KEY, stringCodec, { type: IndexType.KEYWORD });
  public readonly stage = new Attribute("encrypted-stage", stringCodec).plaintext();
  public readonly notes = new AttributeMap("encrypted-notes", noteCodec);
  public readonly messages = new Channel("encrypted-messages", stringCodec);
  public readonly progress = new Stream("encrypted-progress", stringCodec, 1 << 20);
  public readonly child: EncryptedChildFlow;
  private readonly subFlowStep: EncryptedSubFlowStep;
  private readonly start: EncryptedStartStep;

  public constructor(child: EncryptedChildFlow) {
    this.child = child;
    this.subFlowStep = new EncryptedSubFlowStep(child);
    this.start = new EncryptedStartStep(this, this.subFlowStep);
  }

  public getFlowType(): string {
    return "EncryptedRoundTripFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.subFlowStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [
        this.secretNote,
        this.secretCount,
        this.largeNote,
        this.searchStatus,
        this.stage,
        this.notes,
      ],
      channels: [this.messages],
      streams: [this.progress],
    };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public deliver(context: Context, input: string): RPCResult<string> {
    this.messages.publish(context, `message-${input}`);
    return { output: `delivered-${input}|${this.secretNote.get(context)}` };
  }
}

class HeartbeatRetryStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "HeartbeatRetryStep";
  }

  public async execute(context: AsyncContext, input: string): Promise<StepDecision> {
    if (context.attempt === 1) {
      await context.recordHeartbeat({ secret: `heartbeat-${input}` });
      throw new Error(`first attempt declined card 4242 for ${input}`);
    }
    return gracefulComplete(context.getLastHeartbeatValue<{ secret: string }>()?.secret ?? "missing");
  }

  public getStepOptions(): StepOptions {
    return {
      heartbeatTimeoutMs: 10_000,
      executeRetry: { maximumAttempts: 2, initialIntervalMs: 4_000, backoffCoefficient: 1 },
    };
  }
}

export class EncryptedHeartbeatFlow implements Flow<string> {
  private readonly start = new HeartbeatRetryStep();

  public getFlowType(): string {
    return "EncryptedHeartbeatFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}

class RecoverAndFailStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "RecoverAndFailStep";
  }

  public execute(context: Context, _input: string): StepDecision {
    return forceFail(`recovered: ${context.recoveryError?.detail ?? "no recovery error"}`);
  }
}

class AlwaysFailingStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly recover: RecoverAndFailStep) {}

  public getStepType(): string {
    return "AlwaysFailingStep";
  }

  public execute(_context: Context, input: string): StepDecision {
    throw new Error(`recover-me ${input}`);
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: { maximumAttempts: 1, backoffCoefficient: 2 },
      executeFailure: ExecuteFailure.proceedTo(this.recover),
    };
  }
}

export class EncryptedRecoveryFlow implements Flow<string> {
  private readonly recover = new RecoverAndFailStep();
  private readonly start = new AlwaysFailingStep(this.recover);

  public getFlowType(): string {
    return "EncryptedRecoveryFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.recover);
  }
}

class WaitForeverStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly release: Channel<string>) {}

  public getStepType(): string {
    return "WaitForeverStep";
  }

  public waitFor(_context: Context, _input: string): Wait {
    return Wait.until(this.release.forOne());
  }

  public execute(context: Context, _input: string): StepDecision {
    return gracefulComplete(this.release.results(context)[0] ?? "");
  }
}

class ThrowingStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly makeError: (input: string) => unknown) {}

  public getStepType(): string {
    return "ThrowingStep";
  }

  public execute(_context: Context, input: string): StepDecision {
    throw this.makeError(input);
  }
}

/** A Flow whose only Step throws the error that the test builds from the Step input. */
export class ThrowingFlow implements Flow<string> {
  private readonly start: ThrowingStep;

  public constructor(makeError: (input: string) => unknown) {
    this.start = new ThrowingStep(makeError);
  }

  public getFlowType(): string {
    return "EncryptedThrowingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}

export const ENCRYPTED_INDEXED_STATUS_KEY = "EncryptedIndexedStatus";

export class EncryptedMatchFlow implements Flow<string> {
  public readonly encryptedStatus = new Attribute("encrypted-match-status", stringCodec);
  public readonly plaintextStatus = new Attribute("plaintext-match-status", stringCodec).plaintext();
  public readonly indexedStatus = new Attribute(ENCRYPTED_INDEXED_STATUS_KEY, stringCodec, { type: IndexType.KEYWORD });
  public readonly unreadDetails = new Attribute("encrypted-match-unread-details", noteCodec);
  public readonly callCount = new Attribute("encrypted-match-call-count", int64Codec);
  public readonly release = new Channel("encrypted-match-release", stringCodec);
  private readonly start = new WaitForeverStep(this.release);

  public getFlowType(): string {
    return "EncryptedMatchFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.encryptedStatus, this.plaintextStatus, this.indexedStatus, this.unreadDetails, this.callCount],
      channels: [this.release],
    };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public countCall(context: Context, input: string): RPCResult<string> {
    const count = this.callCount.get(context) + 1n;
    this.callCount.set(context, count);
    return { output: `${input}-${count}` };
  }

  @rpc({ inputCodec: stringCodec })
  public setStatuses(context: Context, input: string): void {
    this.encryptedStatus.set(context, input);
    this.plaintextStatus.set(context, input);
    this.indexedStatus.set(context, input);
  }

  @rpc({
    action: new Action({
      label: "Approve",
      requiredPermission: "encrypted.approve",
      condition: whenAttributeMatches(
        new Attribute("plaintext-match-status", stringCodec).plaintext(),
        AttributeMatch.equalTo("ready"),
      ),
    }),
  })
  public approve(context: Context): void {
    this.release.publish(context, "approved");
  }
}

export class EncryptedActionConditionFlow implements Flow<string> {
  public readonly condition = new Attribute("encrypted-action-condition", stringCodec);
  public readonly release = new Channel("encrypted-action-release", stringCodec);
  private readonly start = new WaitForeverStep(this.release);

  public getFlowType(): string {
    return "EncryptedActionConditionFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.condition], channels: [this.release] };
  }

  @rpc({ inputCodec: stringCodec })
  public writeCondition(context: Context, input: string): void {
    this.condition.set(context, input);
  }

  @rpc({
    action: new Action({
      label: "Approve",
      requiredPermission: "encrypted.condition",
      condition: whenAttributeMatches(
        new Attribute("encrypted-action-condition", stringCodec),
        AttributeMatch.equalTo("ready"),
      ),
    }),
  })
  public approve(context: Context): void {
    this.release.publish(context, "approved");
  }
}
