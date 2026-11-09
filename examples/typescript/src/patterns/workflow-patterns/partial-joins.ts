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
  Channel,
  ChannelMap,
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
  withCancelingSteps,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

const BRANCH_LETTERS = ["A", "B", "C"] as const;
const BRANCH_COUNT = BRANCH_LETTERS.length;
type BranchLetter = (typeof BRANCH_LETTERS)[number];

function requireBranchLetter(value: string): BranchLetter {
  const letter = BRANCH_LETTERS.find((candidate) => candidate === value);
  if (letter === undefined) {
    throw new Error(`branch must be one of ${BRANCH_LETTERS.join(", ")}, got ${value}`);
  }
  return letter;
}

// Signals are keyed by round, so a later signal never counts toward an earlier round.
function roundInstance(round: number): string {
  return `round-${round}`;
}

// WCP-9 (requiredQuotes 1) and WCP-30 (requiredQuotes 2).

// A reset Step consumes the late quotes of a round, then re-enables the join.

export interface QuoteRound {
  readonly requiredQuotes: number;
  readonly round: number;
  readonly roundCount: number;
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
const quoteReceived = new ChannelMap("quoteReceived", stringCodec);

class RequestQuotes implements Step<QuoteRound> {
  public getStepType(): string {
    return "RequestQuotes";
  }

  public execute(_context: Context, quoteRound: QuoteRound): StepDecision {
    return goToMany(
      StepMovement.of(QuoteFromSupplierA, quoteRound.round),
      StepMovement.of(QuoteFromSupplierB, quoteRound.round),
      StepMovement.of(QuoteFromSupplierC, quoteRound.round),
      StepMovement.of(AcceptQuotes, quoteRound),
    );
  }
}

class QuoteFromSupplierA implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "QuoteFromSupplierA";
  }

  public waitFor(_context: Context, _round: number): Wait {
    return Wait.until(supplierAReleased.forOne());
  }

  public execute(context: Context, round: number): StepDecision {
    quoteReceived.publish(context, roundInstance(round), "A");
    return deadEnd();
  }
}

class QuoteFromSupplierB implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "QuoteFromSupplierB";
  }

  public waitFor(_context: Context, _round: number): Wait {
    return Wait.until(supplierBReleased.forOne());
  }

  public execute(context: Context, round: number): StepDecision {
    quoteReceived.publish(context, roundInstance(round), "B");
    return deadEnd();
  }
}

class QuoteFromSupplierC implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "QuoteFromSupplierC";
  }

  public waitFor(_context: Context, _round: number): Wait {
    return Wait.until(supplierCReleased.forOne());
  }

  public execute(context: Context, round: number): StepDecision {
    quoteReceived.publish(context, roundInstance(round), "C");
    return deadEnd();
  }
}

class AcceptQuotes implements Step<QuoteRound> {
  public getStepType(): string {
    return "AcceptQuotes";
  }

  public waitFor(_context: Context, quoteRound: QuoteRound): Wait {
    return Wait.until(quoteReceived.forN(roundInstance(quoteRound.round), quoteRound.requiredQuotes));
  }

  public execute(context: Context, quoteRound: QuoteRound): StepDecision {
    const acceptedQuotes: AcceptedQuotes = {
      round: quoteRound.round,
      acceptedQuotes: [...quoteReceived.results(context, roundInstance(quoteRound.round))].sort(),
    };
    return goToMany(
      StepMovement.of(PlaceOrder, acceptedQuotes),
      StepMovement.of(ResetQuoteJoin, quoteRound),
    );
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

class ResetQuoteJoin implements Step<QuoteRound> {
  public getStepType(): string {
    return "ResetQuoteJoin";
  }

  public waitFor(_context: Context, quoteRound: QuoteRound): Wait {
    const remainingQuotes = BRANCH_COUNT - quoteRound.requiredQuotes;
    return Wait.until(quoteReceived.forN(roundInstance(quoteRound.round), remainingQuotes));
  }

  public execute(context: Context, quoteRound: QuoteRound): StepDecision {
    if (quoteRound.round < quoteRound.roundCount) {
      return goTo(RequestQuotes, { ...quoteRound, round: quoteRound.round + 1 });
    }
    const ignoredQuotes: IgnoredQuotes = {
      round: quoteRound.round,
      ignoredQuotes: [...quoteReceived.results(context, roundInstance(quoteRound.round))].sort(),
    };
    return gracefulComplete(ignoredQuotes);
  }
}

export class StructuredPartialJoinFlow implements Flow<QuoteRound> {
  private readonly requestQuotes = new RequestQuotes();
  private readonly quoteFromSupplierA = new QuoteFromSupplierA();
  private readonly quoteFromSupplierB = new QuoteFromSupplierB();
  private readonly quoteFromSupplierC = new QuoteFromSupplierC();
  private readonly acceptQuotes = new AcceptQuotes();
  private readonly placeOrder = new PlaceOrder();
  private readonly resetQuoteJoin = new ResetQuoteJoin();

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
      this.resetQuoteJoin,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [supplierAReleased, supplierBReleased, supplierCReleased, quoteReceived] };
  }

  @rpc({ inputCodec: stringCodec })
  public releaseSupplierQuote(context: Context, supplier: string): void {
    supplierReleased[requireBranchLetter(supplier)].publish(context, "released");
  }
}

// WCP-28 (requiredSignals 1) and WCP-31 (requiredSignals 2).

// No Step waits on round k + 1 before round k resets, so a next-round signal stays queued.

export interface BlockingJoinRound {
  readonly requiredSignals: number;
  readonly round: number;
}

export interface ProcessedRound {
  readonly round: number;
  readonly branches: string[];
}

const branchASignal = new Channel("branchASignal", stringCodec);
const branchBSignal = new Channel("branchBSignal", stringCodec);
const branchCSignal = new Channel("branchCSignal", stringCodec);
const branchSignals = { A: branchASignal, B: branchBSignal, C: branchCSignal };
const branchCompleted = new ChannelMap("branchCompleted", stringCodec);
const BLOCKING_JOIN_ROUNDS = 2;

class StartBlockingRound implements Step<BlockingJoinRound> {
  public getStepType(): string {
    return "StartBlockingRound";
  }

  public execute(_context: Context, joinRound: BlockingJoinRound): StepDecision {
    return goToMany(
      StepMovement.of(SignalFromBranchA, joinRound.round),
      StepMovement.of(SignalFromBranchB, joinRound.round),
      StepMovement.of(SignalFromBranchC, joinRound.round),
      StepMovement.of(BlockingJoin, joinRound),
    );
  }
}

class SignalFromBranchA implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "SignalFromBranchA";
  }

  public waitFor(_context: Context, _round: number): Wait {
    return Wait.until(branchASignal.forOne());
  }

  public execute(context: Context, round: number): StepDecision {
    branchCompleted.publish(context, roundInstance(round), "A");
    return deadEnd();
  }
}

class SignalFromBranchB implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "SignalFromBranchB";
  }

  public waitFor(_context: Context, _round: number): Wait {
    return Wait.until(branchBSignal.forOne());
  }

  public execute(context: Context, round: number): StepDecision {
    branchCompleted.publish(context, roundInstance(round), "B");
    return deadEnd();
  }
}

class SignalFromBranchC implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "SignalFromBranchC";
  }

  public waitFor(_context: Context, _round: number): Wait {
    return Wait.until(branchCSignal.forOne());
  }

  public execute(context: Context, round: number): StepDecision {
    branchCompleted.publish(context, roundInstance(round), "C");
    return deadEnd();
  }
}

class BlockingJoin implements Step<BlockingJoinRound> {
  public getStepType(): string {
    return "BlockingJoin";
  }

  public waitFor(_context: Context, joinRound: BlockingJoinRound): Wait {
    return Wait.until(branchCompleted.forN(roundInstance(joinRound.round), joinRound.requiredSignals));
  }

  public execute(context: Context, joinRound: BlockingJoinRound): StepDecision {
    const processedRound: ProcessedRound = {
      round: joinRound.round,
      branches: [...branchCompleted.results(context, roundInstance(joinRound.round))].sort(),
    };
    return goToMany(
      StepMovement.of(ProcessRound, processedRound),
      StepMovement.of(ResetBlockingJoin, joinRound),
    );
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

class ResetBlockingJoin implements Step<BlockingJoinRound> {
  public getStepType(): string {
    return "ResetBlockingJoin";
  }

  public waitFor(_context: Context, joinRound: BlockingJoinRound): Wait {
    const remainingSignals = BRANCH_COUNT - joinRound.requiredSignals;
    return Wait.until(branchCompleted.forN(roundInstance(joinRound.round), remainingSignals));
  }

  public execute(_context: Context, joinRound: BlockingJoinRound): StepDecision {
    if (joinRound.round < BLOCKING_JOIN_ROUNDS) {
      return goTo(StartBlockingRound, { ...joinRound, round: joinRound.round + 1 });
    }
    return gracefulComplete(joinRound.round);
  }
}

export class BlockingPartialJoinFlow implements Flow<BlockingJoinRound> {
  private readonly startBlockingRound = new StartBlockingRound();
  private readonly signalFromBranchA = new SignalFromBranchA();
  private readonly signalFromBranchB = new SignalFromBranchB();
  private readonly signalFromBranchC = new SignalFromBranchC();
  private readonly blockingJoin = new BlockingJoin();
  private readonly processRound = new ProcessRound();
  private readonly resetBlockingJoin = new ResetBlockingJoin();

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
      this.resetBlockingJoin,
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
const bidReceived = new Channel("bidReceived", stringCodec);
const carrierBookingConfirmed = new Channel("carrierBookingConfirmed", stringCodec);

class RequestBids implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "RequestBids";
  }

  public execute(_context: Context, requiredBids: number): StepDecision {
    return goToMany(
      StepMovement.of(BidFromCarrierA, undefined),
      StepMovement.of(BidFromCarrierB, undefined),
      StepMovement.of(BidFromCarrierC, undefined),
      StepMovement.of(AcceptBids, requiredBids),
    );
  }
}

class BidFromCarrierA implements Step<void> {
  public getStepType(): string {
    return "BidFromCarrierA";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(carrierAReleased.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    bidReceived.publish(context, "A");
    return deadEnd();
  }
}

class BidFromCarrierB implements Step<void> {
  public getStepType(): string {
    return "BidFromCarrierB";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(carrierBReleased.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    bidReceived.publish(context, "B");
    return deadEnd();
  }
}

class BidFromCarrierC implements Step<void> {
  public getStepType(): string {
    return "BidFromCarrierC";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(carrierCReleased.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    bidReceived.publish(context, "C");
    return deadEnd();
  }
}

class AcceptBids implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "AcceptBids";
  }

  public waitFor(_context: Context, requiredBids: number): Wait {
    return Wait.until(bidReceived.forN(requiredBids));
  }

  public execute(context: Context, _requiredBids: number): StepDecision {
    return withCancelingSteps(
      goTo(ConfirmCarrierBooking, [...bidReceived.results(context)].sort()),
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
