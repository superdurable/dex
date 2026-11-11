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

// Workflow Patterns WCP-33 (generalised AND-join), WCP-41 (thread merge), and
// WCP-42 (thread split).

import {
  Channel,
  Join,
  StepList,
  StepMovement,
  Wait,
  doubleCodec,
  goToMany,
  gracefulComplete,
  joinRoundCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type JoinRound,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

// WCP-33 Generalised AND-Join: AllOf fires once per invoice and payment pair.
// Extra messages stay queued for a later firing.

export interface InvoicePaymentMatching {
  readonly expectedMatches: number;
  readonly matchedCount: number;
}

export interface InvoicePaymentMatch {
  readonly invoiceId: string;
  readonly paymentId: string;
}

const invoiceReceived = new Channel("invoiceReceived", stringCodec);
const paymentReceived = new Channel("paymentReceived", stringCodec);

class MatchInvoiceAndPayment implements Step<InvoicePaymentMatching> {
  public getStepType(): string {
    return "MatchInvoiceAndPayment";
  }

  public waitFor(_context: Context, _matching: InvoicePaymentMatching): Wait {
    return Wait.allOf(invoiceReceived.forOne(), paymentReceived.forOne());
  }

  public execute(context: Context, matching: InvoicePaymentMatching): StepDecision {
    const match: InvoicePaymentMatch = {
      invoiceId: invoiceReceived.results(context)[0]!,
      paymentId: paymentReceived.results(context)[0]!,
    };
    const matchedCount = matching.matchedCount + 1;
    if (matchedCount < matching.expectedMatches) {
      return goToMany(
        StepMovement.of(RecordMatch, match),
        StepMovement.of(MatchInvoiceAndPayment, {
          expectedMatches: matching.expectedMatches,
          matchedCount,
        }),
      );
    }
    return goToMany(StepMovement.of(RecordMatch, match));
  }
}

class RecordMatch implements Step<InvoicePaymentMatch> {
  public getStepType(): string {
    return "RecordMatch";
  }

  public execute(_context: Context, match: InvoicePaymentMatch): StepDecision {
    return gracefulComplete(match);
  }
}

export class GeneralizedAndJoinFlow implements Flow<InvoicePaymentMatching> {
  private readonly matchInvoiceAndPayment = new MatchInvoiceAndPayment();
  private readonly recordMatch = new RecordMatch();

  public getFlowType(): string {
    return "GeneralizedAndJoinFlow";
  }

  public getSteps(): StepList<InvoicePaymentMatching> {
    return StepList.startStep(this.matchInvoiceAndPayment).otherSteps(this.recordMatch);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [invoiceReceived, paymentReceived] };
  }

  @rpc({ inputCodec: stringCodec })
  public deliverInvoice(context: Context, invoiceId: string): void {
    invoiceReceived.publish(context, invoiceId);
  }

  @rpc({ inputCodec: stringCodec })
  public deliverPayment(context: Context, paymentId: string): void {
    paymentReceived.publish(context, paymentId);
  }
}

// WCP-42 Thread Split and WCP-41 Thread Merge: the Join forks several threads
// of one Step type, and the merge waits for every thread.

interface ParcelThread {
  readonly parcelIndex: number;
  readonly round: JoinRound;
}

const parcelPacked = new Join("parcelPacked", doubleCodec);

class SplitShipment implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "SplitShipment";
  }

  public execute(context: Context, parcelCount: number): StepDecision {
    return goToMany(
      ...parcelPacked.fork(
        context,
        Array.from(
          { length: parcelCount },
          (_, parcelIndex) => (round: JoinRound) => StepMovement.of(PackParcel, { parcelIndex, round }),
        ),
        (round) => StepMovement.of(MergeParcels, round),
      ),
    );
  }
}

class PackParcel implements Step<ParcelThread> {
  public getStepType(): string {
    return "PackParcel";
  }

  public execute(context: Context, thread: ParcelThread): StepDecision {
    return parcelPacked.endBranch(context, thread.round, thread.parcelIndex);
  }
}

class MergeParcels implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;

  public getStepType(): string {
    return "MergeParcels";
  }

  public waitFor(_context: Context, round: JoinRound): Wait {
    return Wait.until(parcelPacked.forAllBranches(round));
  }

  public execute(context: Context, round: JoinRound): StepDecision {
    return gracefulComplete(
      [...parcelPacked.branchResults(context, round)].sort((left, right) => left - right),
    );
  }
}

export class ThreadSplitMergeFlow implements Flow<number> {
  private readonly splitShipment = new SplitShipment();
  private readonly packParcel = new PackParcel();
  private readonly mergeParcels = new MergeParcels();

  public getFlowType(): string {
    return "ThreadSplitMergeFlow";
  }

  public getSteps(): StepList<number> {
    return StepList.startStep(this.splitShipment).otherSteps(this.packParcel, this.mergeParcels);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [parcelPacked] };
  }
}

export const generalizedAndJoinFlow = new GeneralizedAndJoinFlow();
export const threadSplitMergeFlow = new ThreadSplitMergeFlow();
