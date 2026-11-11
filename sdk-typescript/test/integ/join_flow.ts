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
  Attribute,
  Channel,
  ChannelMap,
  ExecuteFailure,
  Join,
  StepList,
  StepMovement,
  Timer,
  Wait,
  doubleCodec,
  forceComplete,
  goTo,
  deadEnd,
  goToMany,
  gracefulComplete,
  joinRoundCodec,
  jsonCodec,
  rpc,
  stringCodec,
  voidCodec,
  withCancelingSiblingSteps,
  type ChannelMapLoad,
  type Context,
  type Flow,
  type JoinRound,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

export interface BranchInput {
  readonly branchName: string;
  readonly round: JoinRound;
}

export interface JoinOutcome {
  readonly branchCount: number;
  readonly results: readonly string[];
}

const branchInputCodec = jsonCodec<BranchInput>();
const stringListCodec = jsonCodec<readonly string[]>();

/** Ends its branch with its name, optionally after a per-branch release message. */
class JoinBranchStep implements Step<BranchInput> {
  public readonly inputCodec = branchInputCodec;

  public constructor(
    private readonly stepType: string,
    private readonly join: Join<string>,
    private readonly releases?: ChannelMap<string>,
  ) {}

  public getStepType(): string {
    return this.stepType;
  }

  public waitFor(_context: Context, input: BranchInput): Wait {
    return this.releases === undefined
      ? Wait.skipImmediately()
      : Wait.until(this.releases.forOne(input.branchName));
  }

  public execute(context: Context, input: BranchInput): StepDecision {
    return this.join.endBranch(context, input.round, input.branchName);
  }
}

function branchBuilder(step: JoinBranchStep, branchName: string) {
  return (round: JoinRound) => StepMovement.of(step, { branchName, round });
}

// All-of join whose join Step starts waiting only after its branches reported.

const allOfResult = new Join("joinAllOfResult", stringCodec);
const allOfRelease = new ChannelMap("joinAllOfRelease", stringCodec);
const allOfGate = new Channel("joinAllOfGate", stringCodec);

class AllOfSplit implements Step<readonly string[]> {
  public readonly inputCodec = stringListCodec;

  public constructor(private readonly flow: JoinAllOfFlow) {}

  public getStepType(): string {
    return "JoinAllOfSplit";
  }

  public execute(context: Context, branchNames: readonly string[]): StepDecision {
    return goToMany(...allOfResult.fork(
      context,
      branchNames.map((branchName) => branchBuilder(this.flow.branch, branchName)),
      (round) => StepMovement.of(this.flow.gate, round),
    ));
  }
}

/** Holds the round until the test opens the gate, so the join Step starts waiting late. */
class AllOfGate implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public constructor(private readonly flow: JoinAllOfFlow) {}

  public getStepType(): string {
    return "JoinAllOfGate";
  }

  public waitFor(): Wait {
    return Wait.until(allOfGate.forOne());
  }

  public execute(_context: Context, round: JoinRound): StepDecision {
    return goTo(this.flow.accept, round);
  }
}

class AllOfAccept implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "JoinAllOfAccept";
  }

  public waitFor(_context: Context, round: JoinRound): Wait {
    return Wait.until(allOfResult.forAllBranches(round));
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    const outcome: JoinOutcome = {
      branchCount: round.branchCount,
      results: allOfResult.branchResults(context, round),
    };
    return gracefulComplete(outcome);
  }
}

export class JoinAllOfFlow implements Flow<readonly string[]> {
  public readonly branch = new JoinBranchStep("JoinAllOfBranch", allOfResult, allOfRelease);
  public readonly gate = new AllOfGate(this);
  public readonly accept = new AllOfAccept();
  private readonly split = new AllOfSplit(this);

  public getFlowType(): string {
    return "JoinAllOfFlow";
  }

  public getSteps() {
    return StepList.startStep(this.split).otherSteps(this.branch, this.gate, this.accept);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [allOfResult, allOfRelease, allOfGate] };
  }

  @rpc({ inputCodec: stringCodec })
  public releaseBranch(context: Context, branchName: string): void {
    allOfRelease.publish(context, branchName, branchName);
  }

  @rpc()
  public openJoinGate(context: Context): void {
    allOfGate.publish(context, "open");
  }
}

// Partial join over two rounds with a blocking reset in the next split.

export interface PartialRequest {
  readonly requiredResults: number;
  readonly roundNumber: number;
  readonly roundCount: number;
  readonly previousRound?: JoinRound;
  readonly acceptedRounds: readonly (readonly string[])[];
  readonly lateResults: readonly string[];
}

interface PartialJoinInput {
  readonly request: PartialRequest;
  readonly round: JoinRound;
}

export interface PartialState {
  readonly splitExecutions: number;
  readonly pendingReleasesOfA: number;
}

const partialResult = new Join("joinPartialResult", stringCodec);
const partialRelease = new ChannelMap("joinPartialRelease", stringCodec);
const partialSplitExecutions = new Attribute("joinPartialSplitExecutions", doubleCodec);

class PartialSplit implements Step<PartialRequest> {
  public constructor(private readonly flow: JoinPartialFlow) {}

  public getStepType(): string {
    return "JoinPartialSplit";
  }

  public waitFor(_context: Context, request: PartialRequest): Wait {
    if (request.previousRound === undefined) {
      return Wait.skipImmediately();
    }
    return Wait.until(partialResult.forRemainingBranches(request.previousRound, request.requiredResults));
  }

  public execute(context: Context, request: PartialRequest): StepDecision {
    partialSplitExecutions.set(context, partialSplitExecutions.get(context) + 1);
    const lateResults = request.previousRound === undefined
      ? []
      : partialResult.remainingBranchResults(context, request.previousRound);
    const nextRequest: PartialRequest = { ...request, lateResults: [...request.lateResults, ...lateResults] };
    return goToMany(...partialResult.fork(
      context,
      [
        branchBuilder(this.flow.branchA, "A"),
        branchBuilder(this.flow.branchB, "B"),
        branchBuilder(this.flow.branchC, "C"),
      ],
      (round) => StepMovement.of(this.flow.accept, { request: nextRequest, round }),
    ));
  }
}

class PartialAccept implements Step<PartialJoinInput> {
  public constructor(private readonly flow: JoinPartialFlow) {}

  public getStepType(): string {
    return "JoinPartialAccept";
  }

  public waitFor(_context: Context, input: PartialJoinInput): Wait {
    return Wait.until(partialResult.forBranches(input.round, input.request.requiredResults));
  }

  public execute(context: Context, input: PartialJoinInput): StepDecision {
    const request: PartialRequest = {
      ...input.request,
      acceptedRounds: [...input.request.acceptedRounds, partialResult.branchResults(context, input.round)],
    };
    if (request.roundNumber >= request.roundCount) {
      return forceComplete(request);
    }
    return goTo(this.flow.split, { ...request, roundNumber: request.roundNumber + 1, previousRound: input.round });
  }
}

export class JoinPartialFlow implements Flow<PartialRequest> {
  public readonly branchA = new JoinBranchStep("JoinPartialBranchA", partialResult, partialRelease);
  public readonly branchB = new JoinBranchStep("JoinPartialBranchB", partialResult, partialRelease);
  public readonly branchC = new JoinBranchStep("JoinPartialBranchC", partialResult, partialRelease);
  public readonly split = new PartialSplit(this);
  public readonly accept = new PartialAccept(this);

  public getFlowType(): string {
    return "JoinPartialFlow";
  }

  public getSteps() {
    return StepList.startStep(this.split).otherSteps(this.branchA, this.branchB, this.branchC, this.accept);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [partialSplitExecutions], channels: [partialResult, partialRelease] };
  }

  @rpc({ inputCodec: stringCodec })
  public releaseBranch(context: Context, branchName: string): void {
    partialRelease.publish(context, branchName, branchName);
  }

  @rpc()
  public getPartialState(context: Context): RPCResult<PartialState> {
    return {
      output: {
        splitExecutions: partialSplitExecutions.get(context),
        pendingReleasesOfA: partialRelease.size(context, "A"),
      },
    };
  }
}

// One-branch join that must ignore results reported for another round key.

const isolationResult = new Join("joinIsolationResult", stringCodec);
const isolationRelease = new ChannelMap("joinIsolationRelease", stringCodec);

class IsolationSplit implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: JoinIsolationFlow) {}

  public getStepType(): string {
    return "JoinIsolationSplit";
  }

  public execute(context: Context): StepDecision {
    return goToMany(...isolationResult.fork(
      context,
      [branchBuilder(this.flow.branch, "own")],
      (round) => StepMovement.of(this.flow.accept, round),
    ));
  }
}

class IsolationAccept implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "JoinIsolationAccept";
  }

  public waitFor(_context: Context, round: JoinRound): Wait {
    return Wait.until(isolationResult.forAllBranches(round));
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return gracefulComplete(isolationResult.branchResults(context, round));
  }
}

export class JoinIsolationFlow implements Flow<void> {
  public readonly branch = new JoinBranchStep("JoinIsolationBranch", isolationResult, isolationRelease);
  public readonly accept = new IsolationAccept();
  private readonly split = new IsolationSplit(this);

  public getFlowType(): string {
    return "JoinIsolationFlow";
  }

  public getSteps() {
    return StepList.startStep(this.split).otherSteps(this.branch, this.accept);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [isolationResult, isolationRelease] };
  }

  @rpc()
  public reportForeignResult(context: Context): void {
    isolationResult.reportBranchResult(context, { roundKey: "foreign-round", branchCount: 1 }, "foreign");
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public getPendingResultCount(context: Context, roundKey: string): RPCResult<number> {
    return { output: isolationResult.channelMap.size(context, roundKey) };
  }

  @rpc({ inputCodec: stringCodec })
  public releaseBranch(context: Context, branchName: string): void {
    isolationRelease.publish(context, branchName, branchName);
  }
}

// First-of-m join that cancels its sibling branches.

const firstWinResult = new Join("joinFirstWinResult", stringCodec);
const firstWinRelease = new ChannelMap("joinFirstWinRelease", stringCodec);
const firstWinGate = new Channel("joinFirstWinGate", stringCodec);
const firstWinFinish = new Channel("joinFirstWinFinish", stringCodec);

class FirstWinSplit implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: JoinFirstWinFlow) {}

  public getStepType(): string {
    return "JoinFirstWinSplit";
  }

  public execute(context: Context): StepDecision {
    return goToMany(...firstWinResult.fork(
      context,
      [
        branchBuilder(this.flow.branchA, "A"),
        branchBuilder(this.flow.branchB, "B"),
        branchBuilder(this.flow.branchC, "C"),
      ],
      (round) => StepMovement.of(this.flow.accept, round),
    ));
  }
}

class FirstWinAccept implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public constructor(private readonly flow: JoinFirstWinFlow) {}

  public getStepType(): string {
    return "JoinFirstWinAccept";
  }

  public waitFor(_context: Context, round: JoinRound): Wait {
    return Wait.allOf(firstWinGate.forOne(), firstWinResult.forFirstBranch(round));
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    const [winner] = firstWinResult.branchResults(context, round);
    return withCancelingSiblingSteps(
      goTo(this.flow.hold, winner ?? ""),
      this.flow.branchA,
      this.flow.branchB,
      this.flow.branchC,
    );
  }
}

class FirstWinHold implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "JoinFirstWinHold";
  }

  public waitFor(): Wait {
    return Wait.until(firstWinFinish.forOne());
  }

  public execute(_context: Context, winner: string): StepDecision {
    return gracefulComplete(winner);
  }
}

export class JoinFirstWinFlow implements Flow<void> {
  public readonly branchA = new JoinBranchStep("JoinFirstWinBranchA", firstWinResult, firstWinRelease);
  public readonly branchB = new JoinBranchStep("JoinFirstWinBranchB", firstWinResult, firstWinRelease);
  public readonly branchC = new JoinBranchStep("JoinFirstWinBranchC", firstWinResult, firstWinRelease);
  public readonly accept = new FirstWinAccept(this);
  public readonly hold = new FirstWinHold();
  private readonly split = new FirstWinSplit(this);

  public getFlowType(): string {
    return "JoinFirstWinFlow";
  }

  public getSteps() {
    return StepList.startStep(this.split)
      .otherSteps(this.branchA, this.branchB, this.branchC, this.accept, this.hold);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [firstWinResult, firstWinRelease, firstWinGate, firstWinFinish] };
  }

  @rpc({ inputCodec: stringCodec })
  public releaseBranch(context: Context, branchName: string): void {
    firstWinRelease.publish(context, branchName, branchName);
  }

  @rpc()
  public openJoinGate(context: Context): void {
    firstWinGate.publish(context, "open");
  }

  @rpc()
  public finish(context: Context): void {
    firstWinFinish.publish(context, "finish");
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public getPendingResultCount(context: Context, roundKey: string): RPCResult<number> {
    return { output: firstWinResult.channelMap.size(context, roundKey) };
  }

  /** Loads a round's pending results for {@link JoinFirstWinFlow.getPendingResults}. */
  public pendingResultsLoad(roundKey: string): ChannelMapLoad {
    return firstWinResult.channelMap.loadMessages(roundKey);
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringListCodec })
  public getPendingResults(context: Context, roundKey: string): RPCResult<readonly string[]> {
    return {
      output: firstWinResult.channelMap.pendingMessages(context, roundKey).map((message) => message.value),
    };
  }
}

// All-of join that an RPC cancels together with its branches.

const cancelJoinResult = new Join("joinCancelJoinResult", stringCodec);
const cancelJoinRelease = new ChannelMap("joinCancelJoinRelease", stringCodec);
const cancelJoinGate = new Channel("joinCancelJoinGate", stringCodec);

class CancelJoinSplit implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: JoinCancelJoinFlow) {}

  public getStepType(): string {
    return "JoinCancelJoinSplit";
  }

  public execute(context: Context): StepDecision {
    return goToMany(...cancelJoinResult.fork(
      context,
      [branchBuilder(this.flow.branchA, "A"), branchBuilder(this.flow.branchB, "B")],
      (round) => StepMovement.of(this.flow.accept, round),
    ));
  }
}

class CancelJoinAccept implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "JoinCancelJoinAccept";
  }

  public waitFor(_context: Context, round: JoinRound): Wait {
    return Wait.until(cancelJoinResult.forAllBranches(round));
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return gracefulComplete(cancelJoinResult.branchResults(context, round));
  }
}

class CancelJoinFallback implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "JoinCancelJoinFallback";
  }

  public waitFor(): Wait {
    return Wait.until(cancelJoinGate.forOne());
  }

  public execute(): StepDecision {
    return gracefulComplete(["join canceled"]);
  }
}

export class JoinCancelJoinFlow implements Flow<void> {
  public readonly branchA = new JoinBranchStep("JoinCancelJoinBranchA", cancelJoinResult, cancelJoinRelease);
  public readonly branchB = new JoinBranchStep("JoinCancelJoinBranchB", cancelJoinResult, cancelJoinRelease);
  public readonly accept = new CancelJoinAccept();
  public readonly fallback = new CancelJoinFallback();
  private readonly split = new CancelJoinSplit(this);

  public getFlowType(): string {
    return "JoinCancelJoinFlow";
  }

  public getSteps() {
    return StepList.startStep(this.split).otherSteps(this.branchA, this.branchB, this.accept, this.fallback);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [cancelJoinResult, cancelJoinRelease, cancelJoinGate] };
  }

  @rpc()
  public cancelJoinWithBranches(): RPCResult<void> {
    return {
      output: undefined,
      nextSteps: [StepMovement.of(this.fallback, undefined)],
      cancelingSteps: [this.accept, this.branchA, this.branchB],
    };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public getPendingResultCount(context: Context, roundKey: string): RPCResult<number> {
    return { output: cancelJoinResult.channelMap.size(context, roundKey) };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public getPendingReleaseCount(context: Context, branchName: string): RPCResult<number> {
    return { output: cancelJoinRelease.size(context, branchName) };
  }

  @rpc({ inputCodec: stringCodec })
  public releaseBranch(context: Context, branchName: string): void {
    cancelJoinRelease.publish(context, branchName, branchName);
  }

  @rpc()
  public openGate(context: Context): void {
    cancelJoinGate.publish(context, "open");
  }
}

// A Step that forks the same Join twice in one attempt.

const doubleForkResult = new Join("joinDoubleForkResult", stringCodec);

class DoubleForkSplit implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: JoinDoubleForkFlow) {}

  public getStepType(): string {
    return "JoinDoubleForkSplit";
  }

  public getStepOptions(): StepOptions {
    return { executeRetry: { maximumAttempts: 1 }, executeFailure: ExecuteFailure.proceedTo(this.flow.recovery) };
  }

  public execute(context: Context): StepDecision {
    const first = doubleForkResult.fork(context, [], () => StepMovement.of(this.flow.recovery, undefined));
    const second = doubleForkResult.fork(context, [], () => StepMovement.of(this.flow.recovery, undefined));
    return goToMany(...first, ...second);
  }
}

class DoubleForkRecovery implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "JoinDoubleForkRecovery";
  }

  public execute(context: Context): StepDecision {
    return gracefulComplete(context.recoveryError?.detail ?? "no recovery error");
  }
}

export class JoinDoubleForkFlow implements Flow<void> {
  public readonly recovery = new DoubleForkRecovery();
  private readonly split = new DoubleForkSplit(this);

  public getFlowType(): string {
    return "JoinDoubleForkFlow";
  }

  public getSteps() {
    return StepList.startStep(this.split).otherSteps(this.recovery);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [doubleForkResult] };
  }
}

// Rounds started from an RPC.

const rpcForkResult = new Join("joinRpcForkResult", stringCodec);
const rpcForkFinish = new Channel("joinRpcForkFinish", stringCodec);

class RPCForkIdle implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "JoinRPCForkIdle";
  }

  public waitFor(): Wait {
    return Wait.until(rpcForkFinish.forOne());
  }

  public execute(): StepDecision {
    return gracefulComplete("finished");
  }
}

class RPCForkAccept implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "JoinRPCForkAccept";
  }

  public waitFor(_context: Context, round: JoinRound): Wait {
    return Wait.until(rpcForkResult.forAllBranches(round));
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return forceComplete({ roundKey: round.roundKey, results: [...rpcForkResult.branchResults(context, round)].sort() });
  }
}

/** Ends without reporting; an RPC reports this branch's result. */
class RPCForkHandoffBranch implements Step<BranchInput> {
  public readonly inputCodec = branchInputCodec;

  public getStepType(): string {
    return "JoinRPCForkHandoffBranch";
  }

  public execute(): StepDecision {
    return deadEnd();
  }
}

export class JoinRPCForkFlow implements Flow<void> {
  public readonly branchA = new JoinBranchStep("JoinRPCForkBranchA", rpcForkResult);
  public readonly handoffBranch = new RPCForkHandoffBranch();
  public readonly accept = new RPCForkAccept();
  private readonly idle = new RPCForkIdle();

  public getFlowType(): string {
    return "JoinRPCForkFlow";
  }

  public getSteps() {
    return StepList.startStep(this.idle).otherSteps(this.branchA, this.handoffBranch, this.accept);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [rpcForkResult, rpcForkFinish] };
  }

  @rpc({ inputCodec: stringCodec })
  public startRound(context: Context, mode: string): RPCResult<void> {
    const branchBuilders = [
      branchBuilder(this.branchA, "A"),
      (round: JoinRound) => StepMovement.of(this.handoffBranch, { branchName: "B", round }),
    ];
    const joinBuilder = (round: JoinRound) => StepMovement.of(this.accept, round);
    const nextSteps = mode === "fork"
      ? rpcForkResult.fork(context, branchBuilders, joinBuilder)
      : rpcForkResult.forkWithRoundKey(mode === "slash" ? "rpc/round" : "rpc-round-1", branchBuilders, joinBuilder);
    return { output: undefined, nextSteps };
  }

  @rpc({ inputCodec: joinRoundCodec })
  public reportHandoffResult(context: Context, round: JoinRound): void {
    rpcForkResult.reportBranchResult(context, round, "B-from-rpc");
  }
}

// Join Conditions with out-of-range counts fail waitFor.

export type ConditionValidationCase =
  | "forAllBranches"
  | "forBranchesZero"
  | "forBranchesAboveCount"
  | "forRemainingNegative"
  | "forRemainingAboveCount"
  | "forFirstBranchWithoutBranches";

interface ValidationJoinInput {
  readonly validationCase: ConditionValidationCase;
  readonly round: JoinRound;
}

const validationResult = new Join("joinValidationResult", stringCodec);

class ValidationSplit implements Step<ConditionValidationCase> {
  public readonly inputCodec = jsonCodec<ConditionValidationCase>();

  public constructor(private readonly flow: JoinConditionValidationFlow) {}

  public getStepType(): string {
    return "JoinValidationSplit";
  }

  public execute(context: Context, validationCase: ConditionValidationCase): StepDecision {
    return goToMany(...validationResult.fork(
      context,
      validationCase === "forFirstBranchWithoutBranches" ? [] : [branchBuilder(this.flow.branch, "A")],
      (round) => StepMovement.of(this.flow.accept, { validationCase, round }),
    ));
  }
}

class ValidationAccept implements Step<ValidationJoinInput> {
  public getStepType(): string {
    return "JoinValidationAccept";
  }

  public getStepOptions(): StepOptions {
    return { waitForRetry: { maximumAttempts: 1 }, waitForFailure: "proceed" };
  }

  public waitFor(_context: Context, input: ValidationJoinInput): Wait {
    const { round } = input;
    switch (input.validationCase) {
      case "forAllBranches":
        return Wait.until(validationResult.forAllBranches(round));
      case "forBranchesZero":
        return Wait.until(validationResult.forBranches(round, 0));
      case "forBranchesAboveCount":
        return Wait.until(validationResult.forBranches(round, round.branchCount + 1));
      case "forRemainingNegative":
        return Wait.until(validationResult.forRemainingBranches(round, -1));
      case "forRemainingAboveCount":
        return Wait.until(validationResult.forRemainingBranches(round, round.branchCount + 1));
      case "forFirstBranchWithoutBranches":
        return Wait.until(validationResult.forFirstBranch(round));
    }
  }

  public execute(context: Context): StepDecision {
    return gracefulComplete(context.waitForMethodFailed() ? "waitForFailed" : "ready");
  }
}

export class JoinConditionValidationFlow implements Flow<ConditionValidationCase> {
  public readonly branch = new JoinBranchStep("JoinValidationBranch", validationResult);
  public readonly accept = new ValidationAccept();
  private readonly split = new ValidationSplit(this);

  public getFlowType(): string {
    return "JoinConditionValidationFlow";
  }

  public getSteps() {
    return StepList.startStep(this.split).otherSteps(this.branch, this.accept);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [validationResult] };
  }
}

// Zero-count Conditions: zero branches and a reset with nothing remaining.

export interface ZeroOutcome {
  readonly branchCount: number;
  readonly acceptedCount: number;
  readonly remainingCount: number;
}

interface ZeroResetInput {
  readonly round: JoinRound;
  readonly acceptedCount: number;
}

const zeroResult = new Join("joinZeroResult", stringCodec);

class ZeroSplit implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly flow: JoinZeroFlow) {}

  public getStepType(): string {
    return "JoinZeroSplit";
  }

  public execute(context: Context, branchCount: number): StepDecision {
    return goToMany(...zeroResult.fork(
      context,
      Array.from({ length: branchCount }, (_, index) => branchBuilder(this.flow.branch, `branch-${index}`)),
      (round) => StepMovement.of(this.flow.accept, round),
    ));
  }
}

class ZeroAccept implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public constructor(private readonly flow: JoinZeroFlow) {}

  public getStepType(): string {
    return "JoinZeroAccept";
  }

  public waitFor(_context: Context, round: JoinRound): Wait {
    return Wait.until(zeroResult.forAllBranches(round));
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return goTo(this.flow.reset, { round, acceptedCount: zeroResult.branchResults(context, round).length });
  }
}

class ZeroReset implements Step<ZeroResetInput> {
  public getStepType(): string {
    return "JoinZeroReset";
  }

  public waitFor(_context: Context, input: ZeroResetInput): Wait {
    return Wait.until(zeroResult.forRemainingBranches(input.round, input.round.branchCount));
  }

  public execute(context: Context, input: ZeroResetInput): StepDecision {
    const outcome: ZeroOutcome = {
      branchCount: input.round.branchCount,
      acceptedCount: input.acceptedCount,
      remainingCount: zeroResult.remainingBranchResults(context, input.round).length,
    };
    return gracefulComplete(outcome);
  }
}

export class JoinZeroFlow implements Flow<number> {
  public readonly branch = new JoinBranchStep("JoinZeroBranch", zeroResult);
  public readonly accept = new ZeroAccept(this);
  public readonly reset = new ZeroReset();
  private readonly split = new ZeroSplit(this);

  public getFlowType(): string {
    return "JoinZeroFlow";
  }

  public getSteps() {
    return StepList.startStep(this.split).otherSteps(this.branch, this.accept, this.reset);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [zeroResult] };
  }
}

// An all-of join with a Timer deadline, where one branch never reports.

export interface TimerOutcome {
  readonly hasTimerFired: boolean;
  readonly results: readonly string[];
}

const timerResult = new Join("joinTimerResult", stringCodec);
const timerRelease = new ChannelMap("joinTimerRelease", stringCodec);

class TimerSplit implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: JoinTimerFlow) {}

  public getStepType(): string {
    return "JoinTimerSplit";
  }

  public execute(context: Context): StepDecision {
    return goToMany(...timerResult.fork(
      context,
      [branchBuilder(this.flow.reportingBranch, "A"), branchBuilder(this.flow.silentBranch, "B")],
      (round) => StepMovement.of(this.flow.accept, round),
    ));
  }
}

class TimerAccept implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "JoinTimerAccept";
  }

  public waitFor(_context: Context, round: JoinRound): Wait {
    return Wait.anyOf(timerResult.forAllBranches(round), Timer.byDuration(1_000));
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    const outcome: TimerOutcome = {
      hasTimerFired: context.hasTimerFired(),
      results: timerResult.branchResults(context, round),
    };
    return forceComplete(outcome);
  }
}

export class JoinTimerFlow implements Flow<void> {
  public readonly reportingBranch = new JoinBranchStep("JoinTimerReportingBranch", timerResult);
  public readonly silentBranch = new JoinBranchStep("JoinTimerSilentBranch", timerResult, timerRelease);
  public readonly accept = new TimerAccept();
  private readonly split = new TimerSplit(this);

  public getFlowType(): string {
    return "JoinTimerFlow";
  }

  public getSteps() {
    return StepList.startStep(this.split).otherSteps(this.reportingBranch, this.silentBranch, this.accept);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [timerResult, timerRelease] };
  }
}

// A Join and a ChannelMap that share one name.

const conflictingJoin = new Join("joinConflictingName", stringCodec);
const conflictingChannelMap = new ChannelMap("joinConflictingName", stringCodec);

export class JoinNameConflictFlow implements Flow<void> {
  public getFlowType(): string {
    return "JoinNameConflictFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [conflictingJoin, conflictingChannelMap] };
  }
}
