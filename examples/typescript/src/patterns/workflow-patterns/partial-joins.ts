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

// Workflow Patterns discriminators and partial joins: structured (WCP-9,
// WCP-30), blocking (WCP-28, WCP-31), and cancelling (WCP-29, WCP-32).

import {
  Attribute,
  Channel,
  Join,
  StepList,
  StepMovement,
  Wait,
  doubleCodec,
  goTo,
  goToMany,
  gracefulComplete,
  joinRoundCodec,
  jsonCodec,
  rpc,
  stringCodec,
  withCancelingSiblingSteps,
  type Context,
  type Flow,
  type JoinRound,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

const BRANCH_LETTERS = ["A", "B", "C"] as const;
type BranchLetter = (typeof BRANCH_LETTERS)[number];

function requireBranchLetter(value: string): BranchLetter {
  const letter = BRANCH_LETTERS.find((candidate) => candidate === value);
  if (letter === undefined) {
    throw new Error(`branch must be one of ${BRANCH_LETTERS.join(", ")}, got ${value}`);
  }
  return letter;
}

// WCP-9 (requiredQuotes 1) and WCP-30 (requiredQuotes 2).

// The next round's split resets the Join: it consumes the late quotes of the previous round first.

export interface QuoteRound {
  readonly requiredQuotes: number;
  readonly round: number;
  readonly roundCount: number;
  readonly previousRound?: JoinRound;
}

export interface QuoteJoin {
  readonly quoteRound: QuoteRound;
  readonly round: JoinRound;
}

export interface AcceptedQuotes {
  readonly round: number;
  readonly acceptedQuotes: string[];
}

export interface IgnoredQuotes {
  readonly round: number;
  readonly ignoredQuotes: string[];
}

const supplierAReleased = new Channel("supplierAReleased", stringCodec);
const supplierBReleased = new Channel("supplierBReleased", stringCodec);
const supplierCReleased = new Channel("supplierCReleased", stringCodec);
const supplierReleased = { A: supplierAReleased, B: supplierBReleased, C: supplierCReleased };
const quoteReceived = new Join("quoteReceived", stringCodec);
// The late quotes of the most recently reset round.
export const ignoredQuotes = new Attribute(
  "ignoredQuotes",
  jsonCodec<IgnoredQuotes>({ typeName: "IgnoredQuotes" }),
);

class RequestQuotes implements Step<QuoteRound> {
  public getStepType(): string {
    return "RequestQuotes";
  }

  public waitFor(_context: Context, quoteRound: QuoteRound): Wait {
    if (quoteRound.previousRound === undefined) {
      return Wait.skipImmediately();
    }
    return Wait.until(quoteReceived.forRemainingBranches(quoteRound.previousRound, quoteRound.requiredQuotes));
  }

  public execute(context: Context, quoteRound: QuoteRound): StepDecision {
    if (quoteRound.previousRound !== undefined) {
      ignoredQuotes.set(context, {
        round: quoteRound.round - 1,
        ignoredQuotes: [...quoteReceived.remainingBranchResults(context, quoteRound.previousRound)].sort(),
      });
    }
    return goToMany(
      ...quoteReceived.fork(
        context,
        [
          (round) => StepMovement.of(QuoteFromSupplierA, round),
          (round) => StepMovement.of(QuoteFromSupplierB, round),
          (round) => StepMovement.of(QuoteFromSupplierC, round),
        ],
        (round) => StepMovement.of(AcceptQuotes, { quoteRound, round }),
      ),
    );
  }
}

class QuoteFromSupplierA implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "QuoteFromSupplierA";
  }

  public waitFor(_context: Context, _round: JoinRound): Wait {
    return Wait.until(supplierAReleased.forOne());
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return quoteReceived.endBranch(context, round, "A");
  }
}

class QuoteFromSupplierB implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "QuoteFromSupplierB";
  }

  public waitFor(_context: Context, _round: JoinRound): Wait {
    return Wait.until(supplierBReleased.forOne());
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return quoteReceived.endBranch(context, round, "B");
  }
}

class QuoteFromSupplierC implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "QuoteFromSupplierC";
  }

  public waitFor(_context: Context, _round: JoinRound): Wait {
    return Wait.until(supplierCReleased.forOne());
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return quoteReceived.endBranch(context, round, "C");
  }
}

class AcceptQuotes implements Step<QuoteJoin> {
  public getStepType(): string {
    return "AcceptQuotes";
  }

  public waitFor(_context: Context, join: QuoteJoin): Wait {
    return Wait.until(quoteReceived.forBranches(join.round, join.quoteRound.requiredQuotes));
  }

  public execute(context: Context, join: QuoteJoin): StepDecision {
    const { quoteRound, round } = join;
    const acceptedQuotes: AcceptedQuotes = {
      round: quoteRound.round,
      acceptedQuotes: [...quoteReceived.branchResults(context, round)].sort(),
    };
    if (quoteRound.round < quoteRound.roundCount) {
      return goToMany(
        StepMovement.of(PlaceOrder, acceptedQuotes),
        StepMovement.of(RequestQuotes, {
          requiredQuotes: quoteRound.requiredQuotes,
          round: quoteRound.round + 1,
          roundCount: quoteRound.roundCount,
          previousRound: round,
        }),
      );
    }
    // The last round's late quotes stay pending until the Flow closes.
    return goTo(PlaceOrder, acceptedQuotes);
  }
}

class PlaceOrder implements Step<AcceptedQuotes> {
  public getStepType(): string {
    return "PlaceOrder";
  }

  public execute(_context: Context, acceptedQuotes: AcceptedQuotes): StepDecision {
    return gracefulComplete(acceptedQuotes);
  }
}

export class StructuredPartialJoinFlow implements Flow<QuoteRound> {
  private readonly requestQuotes = new RequestQuotes();
  private readonly quoteFromSupplierA = new QuoteFromSupplierA();
  private readonly quoteFromSupplierB = new QuoteFromSupplierB();
  private readonly quoteFromSupplierC = new QuoteFromSupplierC();
  private readonly acceptQuotes = new AcceptQuotes();
  private readonly placeOrder = new PlaceOrder();

  public getFlowType(): string {
    return "StructuredPartialJoinFlow";
  }

  public getSteps(): StepList<QuoteRound> {
    return StepList.startStep(this.requestQuotes).otherSteps(
      this.quoteFromSupplierA,
      this.quoteFromSupplierB,
      this.quoteFromSupplierC,
      this.acceptQuotes,
      this.placeOrder,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [ignoredQuotes],
      channels: [supplierAReleased, supplierBReleased, supplierCReleased, quoteReceived],
    };
  }

  @rpc({ inputCodec: stringCodec })
  public releaseSupplierQuote(context: Context, supplier: string): void {
    supplierReleased[requireBranchLetter(supplier)].publish(context, "released");
  }
}

// WCP-28 (requiredSignals 1) and WCP-31 (requiredSignals 2).

// Round k + 1 starts only after the Join resets round k, so a next-round signal stays queued.

export interface BlockingJoinRound {
  readonly requiredSignals: number;
  readonly round: number;
  readonly previousRound?: JoinRound;
}

export interface BlockingJoinInput {
  readonly blockingRound: BlockingJoinRound;
  readonly round: JoinRound;
}

export interface ProcessedRound {
  readonly round: number;
  readonly branches: string[];
}

const branchASignal = new Channel("branchASignal", stringCodec);
const branchBSignal = new Channel("branchBSignal", stringCodec);
const branchCSignal = new Channel("branchCSignal", stringCodec);
const branchSignals = { A: branchASignal, B: branchBSignal, C: branchCSignal };
const branchCompleted = new Join("branchCompleted", stringCodec);
const BLOCKING_JOIN_ROUNDS = 2;

class StartBlockingRound implements Step<BlockingJoinRound> {
  public getStepType(): string {
    return "StartBlockingRound";
  }

  public waitFor(_context: Context, blockingRound: BlockingJoinRound): Wait {
    if (blockingRound.previousRound === undefined) {
      return Wait.skipImmediately();
    }
    return Wait.until(branchCompleted.forRemainingBranches(blockingRound.previousRound, blockingRound.requiredSignals));
  }

  public execute(context: Context, blockingRound: BlockingJoinRound): StepDecision {
    return goToMany(
      ...branchCompleted.fork(
        context,
        [
          (round) => StepMovement.of(SignalFromBranchA, round),
          (round) => StepMovement.of(SignalFromBranchB, round),
          (round) => StepMovement.of(SignalFromBranchC, round),
        ],
        (round) => StepMovement.of(BlockingJoin, { blockingRound, round }),
      ),
    );
  }
}

class SignalFromBranchA implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "SignalFromBranchA";
  }

  public waitFor(_context: Context, _round: JoinRound): Wait {
    return Wait.until(branchASignal.forOne());
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return branchCompleted.endBranch(context, round, "A");
  }
}

class SignalFromBranchB implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "SignalFromBranchB";
  }

  public waitFor(_context: Context, _round: JoinRound): Wait {
    return Wait.until(branchBSignal.forOne());
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return branchCompleted.endBranch(context, round, "B");
  }
}

class SignalFromBranchC implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "SignalFromBranchC";
  }

  public waitFor(_context: Context, _round: JoinRound): Wait {
    return Wait.until(branchCSignal.forOne());
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return branchCompleted.endBranch(context, round, "C");
  }
}

class BlockingJoin implements Step<BlockingJoinInput> {
  public getStepType(): string {
    return "BlockingJoin";
  }

  public waitFor(_context: Context, input: BlockingJoinInput): Wait {
    return Wait.until(branchCompleted.forBranches(input.round, input.blockingRound.requiredSignals));
  }

  public execute(context: Context, input: BlockingJoinInput): StepDecision {
    const { blockingRound, round } = input;
    const processedRound: ProcessedRound = {
      round: blockingRound.round,
      branches: [...branchCompleted.branchResults(context, round)].sort(),
    };
    if (blockingRound.round < BLOCKING_JOIN_ROUNDS) {
      return goToMany(
        StepMovement.of(ProcessRound, processedRound),
        StepMovement.of(StartBlockingRound, {
          requiredSignals: blockingRound.requiredSignals,
          round: blockingRound.round + 1,
          previousRound: round,
        }),
      );
    }
    // The last round's late signals stay pending until the Flow closes.
    return goTo(ProcessRound, processedRound);
  }
}

class ProcessRound implements Step<ProcessedRound> {
  public getStepType(): string {
    return "ProcessRound";
  }

  public execute(_context: Context, processedRound: ProcessedRound): StepDecision {
    return gracefulComplete(processedRound);
  }
}

export class BlockingPartialJoinFlow implements Flow<BlockingJoinRound> {
  private readonly startBlockingRound = new StartBlockingRound();
  private readonly signalFromBranchA = new SignalFromBranchA();
  private readonly signalFromBranchB = new SignalFromBranchB();
  private readonly signalFromBranchC = new SignalFromBranchC();
  private readonly blockingJoin = new BlockingJoin();
  private readonly processRound = new ProcessRound();

  public getFlowType(): string {
    return "BlockingPartialJoinFlow";
  }

  public getSteps(): StepList<BlockingJoinRound> {
    return StepList.startStep(this.startBlockingRound).otherSteps(
      this.signalFromBranchA,
      this.signalFromBranchB,
      this.signalFromBranchC,
      this.blockingJoin,
      this.processRound,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [branchASignal, branchBSignal, branchCSignal, branchCompleted] };
  }

  @rpc({ inputCodec: stringCodec })
  public sendBranchSignal(context: Context, branch: string): void {
    const letter = requireBranchLetter(branch);
    branchSignals[letter].publish(context, letter);
  }

  @rpc({ loadChannels: [branchASignal, branchBSignal, branchCSignal], outputCodec: doubleCodec })
  public getQueuedBranchSignalCount(context: Context): RPCResult<number> {
    const queued = BRANCH_LETTERS.map((letter) => branchSignals[letter].size(context));
    return { output: queued.reduce((total, size) => total + size, 0) };
  }
}

// WCP-29 (requiredBids 1) and WCP-32 (requiredBids 2): the join cancels the branches still waiting.

const carrierAReleased = new Channel("carrierAReleased", stringCodec);
const carrierBReleased = new Channel("carrierBReleased", stringCodec);
const carrierCReleased = new Channel("carrierCReleased", stringCodec);
const carrierReleased = { A: carrierAReleased, B: carrierBReleased, C: carrierCReleased };
const bidReceived = new Join("bidReceived", stringCodec);
const carrierBookingConfirmed = new Channel("carrierBookingConfirmed", stringCodec);

interface BidJoinInput {
  readonly requiredBids: number;
  readonly round: JoinRound;
}

class RequestBids implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "RequestBids";
  }

  public execute(context: Context, requiredBids: number): StepDecision {
    return goToMany(
      ...bidReceived.fork(
        context,
        [
          (round) => StepMovement.of(BidFromCarrierA, round),
          (round) => StepMovement.of(BidFromCarrierB, round),
          (round) => StepMovement.of(BidFromCarrierC, round),
        ],
        (round) => StepMovement.of(AcceptBids, { requiredBids, round }),
      ),
    );
  }
}

class BidFromCarrierA implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "BidFromCarrierA";
  }

  public waitFor(_context: Context, _round: JoinRound): Wait {
    return Wait.until(carrierAReleased.forOne());
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return bidReceived.endBranch(context, round, "A");
  }
}

class BidFromCarrierB implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "BidFromCarrierB";
  }

  public waitFor(_context: Context, _round: JoinRound): Wait {
    return Wait.until(carrierBReleased.forOne());
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return bidReceived.endBranch(context, round, "B");
  }
}

class BidFromCarrierC implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "BidFromCarrierC";
  }

  public waitFor(_context: Context, _round: JoinRound): Wait {
    return Wait.until(carrierCReleased.forOne());
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return bidReceived.endBranch(context, round, "C");
  }
}

class AcceptBids implements Step<BidJoinInput> {
  public getStepType(): string {
    return "AcceptBids";
  }

  public waitFor(_context: Context, input: BidJoinInput): Wait {
    return Wait.until(bidReceived.forBranches(input.round, input.requiredBids));
  }

  public execute(context: Context, input: BidJoinInput): StepDecision {
    return withCancelingSiblingSteps(
      goTo(ConfirmCarrierBooking, [...bidReceived.branchResults(context, input.round)].sort()),
      BidFromCarrierA,
      BidFromCarrierB,
      BidFromCarrierC,
    );
  }
}

class ConfirmCarrierBooking implements Step<string[]> {
  public getStepType(): string {
    return "ConfirmCarrierBooking";
  }

  public waitFor(_context: Context, _acceptedBids: string[]): Wait {
    return Wait.until(carrierBookingConfirmed.forOne());
  }

  public execute(_context: Context, acceptedBids: string[]): StepDecision {
    return gracefulComplete(acceptedBids);
  }
}

export class CancellingPartialJoinFlow implements Flow<number> {
  private readonly requestBids = new RequestBids();
  private readonly bidFromCarrierA = new BidFromCarrierA();
  private readonly bidFromCarrierB = new BidFromCarrierB();
  private readonly bidFromCarrierC = new BidFromCarrierC();
  private readonly acceptBids = new AcceptBids();
  private readonly confirmCarrierBookingStep = new ConfirmCarrierBooking();

  public getFlowType(): string {
    return "CancellingPartialJoinFlow";
  }

  public getSteps(): StepList<number> {
    return StepList.startStep(this.requestBids).otherSteps(
      this.bidFromCarrierA,
      this.bidFromCarrierB,
      this.bidFromCarrierC,
      this.acceptBids,
      this.confirmCarrierBookingStep,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      channels: [
        carrierAReleased,
        carrierBReleased,
        carrierCReleased,
        bidReceived,
        carrierBookingConfirmed,
      ],
    };
  }

  @rpc({ inputCodec: stringCodec })
  public releaseCarrierBid(context: Context, carrier: string): void {
    carrierReleased[requireBranchLetter(carrier)].publish(context, "released");
  }

  // A canceled branch no longer waits, so its late release stays queued.
  @rpc({
    loadChannels: [carrierAReleased, carrierBReleased, carrierCReleased],
    outputCodec: doubleCodec,
  })
  public getQueuedCarrierReleaseCount(context: Context): RPCResult<number> {
    const queued = BRANCH_LETTERS.map((letter) => carrierReleased[letter].size(context));
    return { output: queued.reduce((total, size) => total + size, 0) };
  }

  @rpc()
  public confirmCarrierBooking(context: Context): void {
    carrierBookingConfirmed.publish(context, "confirmed");
  }
}

export const structuredPartialJoinFlow = new StructuredPartialJoinFlow();
export const blockingPartialJoinFlow = new BlockingPartialJoinFlow();
export const cancellingPartialJoinFlow = new CancellingPartialJoinFlow();
