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
  StepMovement,
  Wait,
  deadEnd,
  goTo,
  goToMany,
  rpc,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import {
  AwaitOfferResponse,
  CloseRejectedOffer,
  ExpireOffer,
  FulfillOffer,
  RouteResponseBeforeExpiry,
  offerAccepted,
  offerRejected,
  offerResponseWindow,
  publishWhileWindowOpen,
} from "../../src/patterns/deferred-choice/offer-response-flow.js";

const probeGateOpened = new Channel("probeGateOpened", voidCodec);

/** Lets a test hold the window lock inside a response RPC until it releases it. */
export class HeldOfferResponse {
  public readonly entered: Promise<void>;
  public readonly released: Promise<void>;
  private markEntered: () => void = () => {};
  private markReleased: () => void = () => {};

  public constructor() {
    this.entered = new Promise((resolve) => {
      this.markEntered = resolve;
    });
    this.released = new Promise((resolve) => {
      this.markReleased = resolve;
    });
  }

  public enter(): void {
    this.markEntered();
  }

  public release(): void {
    this.markReleased();
  }
}

let heldOfferResponse: HeldOfferResponse | undefined;

/** Creates fresh held-response state; call it before invoking the held RPC. */
export function holdNextOfferResponse(): HeldOfferResponse {
  heldOfferResponse = new HeldOfferResponse();
  return heldOfferResponse;
}

class OpenOfferProbe implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "OpenOfferProbe";
  }

  public execute(_context: Context, offerId: string): StepDecision {
    return goToMany(
      StepMovement.of(AwaitOfferResponse, offerId),
      StepMovement.of(HoldOfferProbeOpen, undefined),
    );
  }
}

/** Keeps the Flow open until the test opens the gate. */
class HoldOfferProbeOpen implements Step<void> {
  public getStepType(): string {
    return "HoldOfferProbeOpen";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(probeGateOpened.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return deadEnd();
  }
}

/**
 * Runs the Decision with Deadline Steps beside a gated Step, so the Flow stays
 * open after the decision and late responses reach the window check.
 */
export class OfferResponseWindowProbeFlow implements Flow<string> {
  public getFlowType(): string {
    return "OfferResponseWindowProbeFlow";
  }

  public getSteps(): StepList<string> {
    return StepList.startStep(new OpenOfferProbe()).otherSteps(
      new HoldOfferProbeOpen(),
      new AwaitOfferResponse(),
      new FulfillOffer(),
      new CloseRejectedOffer(),
      new RouteResponseBeforeExpiry(),
      new ExpireOffer(),
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [offerResponseWindow],
      channels: [probeGateOpened, offerAccepted, offerRejected],
    };
  }

  @rpc({
    inputCodec: stringCodec,
    outputCodec: stringCodec,
    lockAttributes: [offerResponseWindow.lock()],
  })
  public acceptProbeOffer(context: Context, note: string): RPCResult<string> {
    return publishWhileWindowOpen(context, offerAccepted, note);
  }

  @rpc({
    inputCodec: stringCodec,
    outputCodec: stringCodec,
    lockAttributes: [offerResponseWindow.lock()],
  })
  public rejectProbeOffer(context: Context, note: string): RPCResult<string> {
    return publishWhileWindowOpen(context, offerRejected, note);
  }

  /** Holds the window lock until the test releases it. */
  @rpc({
    inputCodec: stringCodec,
    outputCodec: stringCodec,
    lockAttributes: [offerResponseWindow.lock()],
  })
  public async acceptProbeOfferWhenReleased(
    context: Context,
    note: string,
  ): Promise<RPCResult<string>> {
    const held = heldOfferResponse;
    if (held === undefined) {
      throw new Error("no held response is set");
    }
    held.enter();
    await held.released;
    return publishWhileWindowOpen(context, offerAccepted, note);
  }

  @rpc()
  public openProbeGate(context: Context): void {
    probeGateOpened.publish(context, undefined);
  }
}

export const offerResponseWindowProbeFlow = new OfferResponseWindowProbeFlow();

const decisionGateOpened = new Channel("decisionGateOpened", voidCodec);

/** Holds the offer before AwaitOfferResponse starts, until the test opens the decision gate. */
class HoldOfferDecision implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "HoldOfferDecision";
  }

  public waitFor(_context: Context, _offerId: string): Wait {
    return Wait.until(decisionGateOpened.forOne());
  }

  public execute(_context: Context, offerId: string): StepDecision {
    return goTo(AwaitOfferResponse, offerId);
  }
}

/**
 * Starts AwaitOfferResponse only after the test opens a gate, so two replies
 * reach the window before any wait or Execute runs.
 */
export class OfferDecisionGateProbeFlow implements Flow<string> {
  public getFlowType(): string {
    return "OfferDecisionGateProbeFlow";
  }

  public getSteps(): StepList<string> {
    return StepList.startStep(new HoldOfferDecision()).otherSteps(
      new AwaitOfferResponse(),
      new FulfillOffer(),
      new CloseRejectedOffer(),
      new RouteResponseBeforeExpiry(),
      new ExpireOffer(),
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [offerResponseWindow],
      channels: [decisionGateOpened, offerAccepted, offerRejected],
    };
  }

  @rpc({
    inputCodec: stringCodec,
    outputCodec: stringCodec,
    lockAttributes: [offerResponseWindow.lock()],
  })
  public acceptGatedOffer(context: Context, note: string): RPCResult<string> {
    return publishWhileWindowOpen(context, offerAccepted, note);
  }

  @rpc({
    inputCodec: stringCodec,
    outputCodec: stringCodec,
    lockAttributes: [offerResponseWindow.lock()],
  })
  public rejectGatedOffer(context: Context, note: string): RPCResult<string> {
    return publishWhileWindowOpen(context, offerRejected, note);
  }

  @rpc()
  public openDecisionGate(context: Context): void {
    decisionGateOpened.publish(context, undefined);
  }
}

export const offerDecisionGateProbeFlow = new OfferDecisionGateProbeFlow();
