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

import {
  Channel,
  StepList,
  Timer,
  Wait,
  goTo,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

/** How long the customer has to respond. */
export const OFFER_EXPIRY_MS = 3 * 24 * 60 * 60 * 1000;

export const offerAccepted = new Channel("offerAccepted", stringCodec);
export const offerRejected = new Channel("offerRejected", stringCodec);

/** Waits for whichever event happens first and routes on it. */
class AwaitOfferResponse implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "AwaitOfferResponse";
  }

  public waitFor(_context: Context, _offerId: string): Wait {
    return Wait.anyOf(
      offerAccepted.forOne(),
      offerRejected.forOne(),
      Timer.byDuration(OFFER_EXPIRY_MS),
    );
  }

  public execute(context: Context, offerId: string): StepDecision {
    if (context.hasTimerFired()) {
      return goTo(ExpireOffer, offerId);
    }
    if (offerAccepted.results(context).length > 0) {
      return goTo(FulfillOffer, offerId);
    }
    return goTo(CloseRejectedOffer, offerId);
  }
}

class FulfillOffer implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "FulfillOffer";
  }

  public execute(_context: Context, offerId: string): StepDecision {
    return gracefulComplete(`fulfilled ${offerId}`);
  }
}

class CloseRejectedOffer implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "CloseRejectedOffer";
  }

  public execute(_context: Context, offerId: string): StepDecision {
    return gracefulComplete(`rejected ${offerId}`);
  }
}

class ExpireOffer implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "ExpireOffer";
  }

  public execute(_context: Context, offerId: string): StepDecision {
    return gracefulComplete(`expired ${offerId}`);
  }
}

/**
 * Routes on the first of three events: the customer accepts, the customer
 * rejects, or the offer expires.
 */
export class OfferResponseFlow implements Flow<string> {
  public getFlowType(): string {
    return "OfferResponseFlow";
  }

  public getSteps(): StepList<string> {
    return StepList.startStep(new AwaitOfferResponse()).otherSteps(
      new FulfillOffer(),
      new CloseRejectedOffer(),
      new ExpireOffer(),
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [offerAccepted, offerRejected] };
  }

  /** Records that the customer accepted the offer. */
  @rpc({ inputCodec: stringCodec })
  public acceptOffer(context: Context, note: string): void {
    offerAccepted.publish(context, note);
  }

  /** Records that the customer rejected the offer. */
  @rpc({ inputCodec: stringCodec })
  public rejectOffer(context: Context, note: string): void {
    offerRejected.publish(context, note);
  }
}

export const offerResponseFlow = new OfferResponseFlow();
