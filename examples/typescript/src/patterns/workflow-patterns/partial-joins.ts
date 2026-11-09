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
  ConditionCombination,
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

// Channel messages persist, so a reset Step consumes the late quotes.

const supplierAReleased = new Channel("supplierAReleased", stringCodec);
const supplierBReleased = new Channel("supplierBReleased", stringCodec);
const supplierCReleased = new Channel("supplierCReleased", stringCodec);
const quoteReceived = new Channel("quoteReceived", stringCodec);
const SUPPLIER_COUNT = BRANCH_LETTERS.length;

class RequestQuotes implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "RequestQuotes";
  }

  public execute(_context: Context, requiredQuotes: number): StepDecision {
    return goToMany(
      StepMovement.of(QuoteFromSupplierA, undefined),
      StepMovement.of(QuoteFromSupplierB, undefined),
      StepMovement.of(QuoteFromSupplierC, undefined),
      StepMovement.of(AcceptQuotes, requiredQuotes),
    );
  }
}

class QuoteFromSupplierA implements Step<void> {
  public getStepType(): string {
    return "QuoteFromSupplierA";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(supplierAReleased.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    quoteReceived.publish(context, "A");
    return deadEnd();
  }
}

class QuoteFromSupplierB implements Step<void> {
  public getStepType(): string {
    return "QuoteFromSupplierB";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(supplierBReleased.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    quoteReceived.publish(context, "B");
    return deadEnd();
  }
}

class QuoteFromSupplierC implements Step<void> {
  public getStepType(): string {
    return "QuoteFromSupplierC";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(supplierCReleased.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    quoteReceived.publish(context, "C");
    return deadEnd();
  }
}

class AcceptQuotes implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "AcceptQuotes";
  }

  public waitFor(_context: Context, requiredQuotes: number): Wait {
    return Wait.until(quoteReceived.forN(requiredQuotes));
  }

  public execute(context: Context, requiredQuotes: number): StepDecision {
    return goToMany(
      StepMovement.of(PlaceOrder, [...quoteReceived.results(context)].sort()),
      StepMovement.of(ResetQuoteJoin, SUPPLIER_COUNT - requiredQuotes),
    );
  }
}

class PlaceOrder implements Step<string[]> {
  public getStepType(): string {
    return "PlaceOrder";
  }

  public execute(_context: Context, acceptedQuotes: string[]): StepDecision {
    return gracefulComplete(acceptedQuotes);
  }
}

class ResetQuoteJoin implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ResetQuoteJoin";
  }

  public waitFor(_context: Context, remainingQuotes: number): Wait {
    return Wait.until(quoteReceived.forN(remainingQuotes));
  }

  public execute(context: Context, _remainingQuotes: number): StepDecision {
    return gracefulComplete([...quoteReceived.results(context)].sort());
  }
}

export class StructuredPartialJoinFlow implements Flow<number> {
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

  public getSteps(): StepList<number> {
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
    const releasedChannels = { A: supplierAReleased, B: supplierBReleased, C: supplierCReleased };
    releasedChannels[requireBranchLetter(supplier)].publish(context, "released");
  }
}

// WCP-28 (requiredSignals 1) and WCP-31 (requiredSignals 2).

// Per-branch Channels queue next-round signals until the reset Step re-arms the join.

export interface BlockingJoinRound {
  readonly requiredSignals: number;
  readonly round: number;
}

export interface ProcessedRound {
  readonly round: number;
  readonly branches: string[];
}

export interface PendingJoinReset {
  readonly requiredSignals: number;
  readonly round: number;
  readonly pendingBranches: string[];
}

const branchASignal = new Channel("branchASignal", stringCodec);
const branchBSignal = new Channel("branchBSignal", stringCodec);
const branchCSignal = new Channel("branchCSignal", stringCodec);
const branchSignals = { A: branchASignal, B: branchBSignal, C: branchCSignal };
const BLOCKING_JOIN_ROUNDS = 2;

class BlockingJoin implements Step<BlockingJoinRound> {
  public getStepType(): string {
    return "BlockingJoin";
  }

  public waitFor(_context: Context, join: BlockingJoinRound): Wait {
    if (join.requiredSignals === 1) {
      return Wait.anyOf(branchASignal.forOne(), branchBSignal.forOne(), branchCSignal.forOne());
    }
    return Wait.anyCombinationOf(
      ConditionCombination.of(branchASignal.forOne("ab-a"), branchBSignal.forOne("ab-b")),
      ConditionCombination.of(branchASignal.forOne("ac-a"), branchCSignal.forOne("ac-c")),
      ConditionCombination.of(branchBSignal.forOne("bc-b"), branchCSignal.forOne("bc-c")),
    );
  }

  public execute(context: Context, join: BlockingJoinRound): StepDecision {
    const fired = BRANCH_LETTERS.filter((letter) => branchSignals[letter].results(context).length > 0);
    const pending = BRANCH_LETTERS.filter((letter) => !fired.includes(letter));
    return goToMany(
      StepMovement.of(ProcessRound, { round: join.round, branches: [...fired] }),
      StepMovement.of(ResetBlockingJoin, {
        requiredSignals: join.requiredSignals,
        round: join.round,
        pendingBranches: [...pending],
      }),
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

class ResetBlockingJoin implements Step<PendingJoinReset> {
  public getStepType(): string {
    return "ResetBlockingJoin";
  }

  public waitFor(_context: Context, reset: PendingJoinReset): Wait {
    return Wait.allOf(
      ...reset.pendingBranches.map((branch) => branchSignals[requireBranchLetter(branch)].forOne()),
    );
  }

  public execute(_context: Context, reset: PendingJoinReset): StepDecision {
    if (reset.round < BLOCKING_JOIN_ROUNDS) {
      return goTo(BlockingJoin, { requiredSignals: reset.requiredSignals, round: reset.round + 1 });
    }
    return gracefulComplete(reset.round);
  }
}

export class BlockingPartialJoinFlow implements Flow<BlockingJoinRound> {
  private readonly blockingJoin = new BlockingJoin();
  private readonly processRound = new ProcessRound();
  private readonly resetBlockingJoin = new ResetBlockingJoin();

  public getFlowType(): string {
    return "BlockingPartialJoinFlow";
  }

  public getSteps(): StepList<BlockingJoinRound> {
    return StepList.startStep(this.blockingJoin).otherSteps(
      this.processRound,
      this.resetBlockingJoin,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [branchASignal, branchBSignal, branchCSignal] };
  }

  @rpc({ inputCodec: stringCodec })
  public sendBranchSignal(context: Context, branch: string): void {
    const letter = requireBranchLetter(branch);
    branchSignals[letter].publish(context, letter);
  }
}

// WCP-29 Cancelling Discriminator (requiredBids 1) and WCP-32 Cancelling
// Partial Join (requiredBids 2): the join cancels the branches still running.

const carrierAReleased = new Channel("carrierAReleased", stringCodec);
const carrierBReleased = new Channel("carrierBReleased", stringCodec);
const carrierCReleased = new Channel("carrierCReleased", stringCodec);
const bidReceived = new Channel("bidReceived", stringCodec);

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
      gracefulComplete([...bidReceived.results(context)].sort()),
      BidFromCarrierA,
      BidFromCarrierB,
      BidFromCarrierC,
    );
  }
}

export class CancellingPartialJoinFlow implements Flow<number> {
  private readonly requestBids = new RequestBids();
  private readonly bidFromCarrierA = new BidFromCarrierA();
  private readonly bidFromCarrierB = new BidFromCarrierB();
  private readonly bidFromCarrierC = new BidFromCarrierC();
  private readonly acceptBids = new AcceptBids();

  public getFlowType(): string {
    return "CancellingPartialJoinFlow";
  }

  public getSteps(): StepList<number> {
    return StepList.startStep(this.requestBids).otherSteps(
      this.bidFromCarrierA,
      this.bidFromCarrierB,
      this.bidFromCarrierC,
      this.acceptBids,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [carrierAReleased, carrierBReleased, carrierCReleased, bidReceived] };
  }

  @rpc({ inputCodec: stringCodec })
  public releaseCarrierBid(context: Context, carrier: string): void {
    const releasedChannels = { A: carrierAReleased, B: carrierBReleased, C: carrierCReleased };
    releasedChannels[requireBranchLetter(carrier)].publish(context, "released");
  }
}

export const structuredPartialJoinFlow = new StructuredPartialJoinFlow();
export const blockingPartialJoinFlow = new BlockingPartialJoinFlow();
export const cancellingPartialJoinFlow = new CancellingPartialJoinFlow();
