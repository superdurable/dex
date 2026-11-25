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
  Attribute,
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
  type RetryPolicy,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

/** How long the customer has to respond. */
export const OFFER_EXPIRY_MS = 3 * 24 * 60 * 60 * 1000;

/** The first reply or AwaitOfferResponse closes the response window. Unset reads as open. */
export const RESPONSE_WINDOW_OPEN = "open";
export const RESPONSE_WINDOW_CLOSED = "closed";

/** The outcomes of a customer response RPC. */
export const OFFER_RESPONSE_RECORDED = "recorded";
export const OFFER_RESPONSE_WINDOW_CLOSED = "window closed";

export const offerAccepted = new Channel("offerAccepted", stringCodec);
export const offerRejected = new Channel("offerRejected", stringCodec);
/** Locked by both response RPCs and by AwaitOfferResponse.execute. */
export const offerResponseWindow = new Attribute(
  "offerResponseWindow",
  stringCodec,
);

/** Responses get lock conflicts while the locked execute retries, so its retry budget stays short. */
export const AWAIT_OFFER_RESPONSE_EXECUTE_RETRY: RetryPolicy = {
  initialIntervalMs: 1000,
  maximumAttempts: 3,
  totalDurationMs: 60 * 1000,
};
export const AWAIT_OFFER_RESPONSE_EXECUTE_TIMEOUT_MS = 10 * 1000;

/**
 * Publishes the first response and closes the window. The RPC lock commits both with the check.
 */
export function publishWhileWindowOpen(
  context: Context,
  response: Channel<string>,
  note: string,
): RPCResult<string> {
  if (readResponseWindow(context) === RESPONSE_WINDOW_CLOSED) {
    return { output: OFFER_RESPONSE_WINDOW_CLOSED };
  }
  response.publish(context, note);
  // The first reply closes the window, so a second reply cannot also be recorded.
  offerResponseWindow.set(context, RESPONSE_WINDOW_CLOSED);
  return { output: OFFER_RESPONSE_RECORDED };
}

function readResponseWindow(context: Context): string {
  return offerResponseWindow.get(context) ?? RESPONSE_WINDOW_OPEN;
}

// dex:group group-id:decide group-label:"Decide"
// dex:explanation text:"Waits for an acceptance, a rejection, or the 3-day deadline, closes the response window, and routes on the winner."
export class AwaitOfferResponse implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "AwaitOfferResponse";
  }

  /** Locks the response window in execute, the same lock as the response RPCs. */
  public getStepOptions(): StepOptions {
    return {
      executeLockAttributes: [offerResponseWindow.lock()],
      executeRetry: AWAIT_OFFER_RESPONSE_EXECUTE_RETRY,
      executeMethodTimeoutMs: AWAIT_OFFER_RESPONSE_EXECUTE_TIMEOUT_MS,
    };
  }

  public waitFor(_context: Context, _offerId: string): Wait {
    return Wait.anyOf(
      offerAccepted.forOne(),
      offerRejected.forOne(),
      Timer.byDuration(OFFER_EXPIRY_MS),
    );
  }

  public execute(context: Context, offerId: string): StepDecision {
    // Closes the window when the Timer won and no reply closed it first.
    offerResponseWindow.set(context, RESPONSE_WINDOW_CLOSED);
    if (context.hasTimerFired()) {
      // A response committed after the Timer won but before this execute took the lock.
      if (offerAccepted.size(context) + offerRejected.size(context) > 0) {
        return goTo(RouteResponseBeforeExpiry, offerId);
      }
      return goTo(ExpireOffer, offerId);
    }
    if (offerAccepted.results(context).length > 0) {
      return goTo(FulfillOffer, offerId);
    }
    return goTo(CloseRejectedOffer, offerId);
  }
}

// dex:group group-id:decide group-label:"Decide"
// dex:explanation text:"Takes the response that the customer sent before the window closed and routes on it."
export class RouteResponseBeforeExpiry implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "RouteResponseBeforeExpiry";
  }

  public waitFor(_context: Context, _offerId: string): Wait {
    return Wait.anyOf(offerAccepted.forOne(), offerRejected.forOne());
  }

  public execute(context: Context, offerId: string): StepDecision {
    if (offerAccepted.results(context).length > 0) {
      return goTo(FulfillOffer, offerId);
    }
    return goTo(CloseRejectedOffer, offerId);
  }
}

// dex:group group-id:outcome group-label:"Outcome"
// dex:explanation text:"Completes the Flow with the fulfilled offer."
export class FulfillOffer implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "FulfillOffer";
  }

  public execute(_context: Context, offerId: string): StepDecision {
    return gracefulComplete(`fulfilled ${offerId}`);
  }
}

// dex:group group-id:outcome group-label:"Outcome"
// dex:explanation text:"Completes the Flow with the rejected offer."
export class CloseRejectedOffer implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "CloseRejectedOffer";
  }

  public execute(_context: Context, offerId: string): StepDecision {
    return gracefulComplete(`rejected ${offerId}`);
  }
}

// dex:group group-id:outcome group-label:"Outcome"
// dex:explanation text:"Completes the Flow with the expired offer."
export class ExpireOffer implements Step<string> {
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
      new RouteResponseBeforeExpiry(),
      new ExpireOffer(),
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [offerResponseWindow],
      channels: [offerAccepted, offerRejected],
    };
  }

  /**
   * Records that the customer accepted the offer.
   * Returns OFFER_RESPONSE_WINDOW_CLOSED after the first reply or the decision.
   */
  @rpc({
    inputCodec: stringCodec,
    outputCodec: stringCodec,
    lockAttributes: [offerResponseWindow.lock()],
  })
  public acceptCustomerOffer(
    context: Context,
    note: string,
  ): RPCResult<string> {
    return publishWhileWindowOpen(context, offerAccepted, note);
  }

  /**
   * Records that the customer rejected the offer.
   * Returns OFFER_RESPONSE_WINDOW_CLOSED after the first reply or the decision.
   */
  @rpc({
    inputCodec: stringCodec,
    outputCodec: stringCodec,
    lockAttributes: [offerResponseWindow.lock()],
  })
  public rejectCustomerOffer(
    context: Context,
    note: string,
  ): RPCResult<string> {
    return publishWhileWindowOpen(context, offerRejected, note);
  }
}

export const offerResponseFlow = new OfferResponseFlow();
