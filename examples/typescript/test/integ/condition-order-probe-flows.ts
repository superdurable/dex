/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
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

// The probe Flows hold their first wait behind a gate, so both competing
// messages are pending before the AnyOf registers.

import {
  Channel,
  StepList,
  Timer,
  Wait,
  goTo,
  gracefulComplete,
  rpc,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import {
  applicationCanceled,
  underwriterDecided,
} from "../../src/patterns/cancel-case/loan-application-flow.js";
import {
  OFFER_EXPIRY_MS,
  offerAccepted,
  offerRejected,
} from "../../src/patterns/deferred-choice/offer-response-flow.js";

const probeGateOpened = new Channel("probeGateOpened", voidCodec);

/** Reports which condition won and how many messages the other left pending. */
export interface ConditionOrderOutcome {
  readonly winner: string;
  readonly loserPendingCount: number;
}

class HoldOfferProbe implements Step<void> {
  public getStepType(): string {
    return "HoldOfferProbe";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(probeGateOpened.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(AwaitOfferProbeResponse, undefined);
  }
}

/** Declares its conditions in the same order as AwaitOfferResponse. */
class AwaitOfferProbeResponse implements Step<void> {
  public getStepType(): string {
    return "AwaitOfferProbeResponse";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(
      offerAccepted.forOne(),
      offerRejected.forOne(),
      Timer.byDuration(OFFER_EXPIRY_MS),
    );
  }

  public execute(context: Context, _input: void): StepDecision {
    if (offerAccepted.results(context).length > 0) {
      return gracefulComplete({
        winner: "accepted",
        loserPendingCount: offerRejected.size(context),
      });
    }
    return gracefulComplete({
      winner: "rejected",
      loserPendingCount: offerAccepted.size(context),
    });
  }
}

/** Waits like AwaitOfferResponse, after a gate. */
export class DeferredChoiceOrderProbeFlow implements Flow<void> {
  public getFlowType(): string {
    return "DeferredChoiceOrderProbeFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(new HoldOfferProbe()).otherSteps(new AwaitOfferProbeResponse());
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [probeGateOpened, offerAccepted, offerRejected] };
  }

  @rpc({ inputCodec: stringCodec })
  public acceptProbeOffer(context: Context, note: string): void {
    offerAccepted.publish(context, note);
  }

  @rpc({ inputCodec: stringCodec })
  public rejectProbeOffer(context: Context, note: string): void {
    offerRejected.publish(context, note);
  }

  @rpc()
  public openProbeGate(context: Context): void {
    probeGateOpened.publish(context, undefined);
  }
}

class HoldDecisionProbe implements Step<void> {
  public getStepType(): string {
    return "HoldDecisionProbe";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(probeGateOpened.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(AwaitProbeDecision, undefined);
  }
}

/** Declares its conditions in the same order as AwaitUnderwriterDecision. */
class AwaitProbeDecision implements Step<void> {
  public getStepType(): string {
    return "AwaitProbeDecision";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(applicationCanceled.forOne(), underwriterDecided.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    if (applicationCanceled.results(context).length > 0) {
      return gracefulComplete({
        winner: "canceled",
        loserPendingCount: underwriterDecided.size(context),
      });
    }
    return gracefulComplete({
      winner: "decided",
      loserPendingCount: applicationCanceled.size(context),
    });
  }
}

/** Waits like AwaitUnderwriterDecision, after a gate. */
export class CancelCaseOrderProbeFlow implements Flow<void> {
  public getFlowType(): string {
    return "CancelCaseOrderProbeFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(new HoldDecisionProbe()).otherSteps(new AwaitProbeDecision());
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [probeGateOpened, applicationCanceled, underwriterDecided] };
  }

  @rpc({ inputCodec: stringCodec })
  public cancelProbeApplication(context: Context, reason: string): void {
    applicationCanceled.publish(context, reason);
  }

  @rpc({ inputCodec: stringCodec })
  public decideProbeApplication(context: Context, decision: string): void {
    underwriterDecided.publish(context, decision);
  }

  @rpc()
  public openProbeGate(context: Context): void {
    probeGateOpened.publish(context, undefined);
  }
}

export const deferredChoiceOrderProbeFlow = new DeferredChoiceOrderProbeFlow();
export const cancelCaseOrderProbeFlow = new CancelCaseOrderProbeFlow();
