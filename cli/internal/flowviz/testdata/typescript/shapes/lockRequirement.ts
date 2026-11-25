// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Attribute,
  AttributeMap,
  Channel,
  ChannelMap,
  StepList,
  booleanCodec,
  doubleCodec,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

const cartLock = new Attribute("CartLock", booleanCodec);
const cartTotal = new Attribute("CartTotal", doubleCodec).writesRequireLock().plaintext();
const cartItems = new AttributeMap("CartItems", doubleCodec).writesRequireAttributeLock(cartLock);
const replies = new Channel("Replies", stringCodec).publishesRequireAttributeLock(cartLock);
const events = new ChannelMap("Events", stringCodec).publishesRequireAttributeLock(cartLock);

// dex:group group-id:cart group-label:"Cart"
// dex:explanation text:"Add one item to the cart under the cart lock."
class AddItem implements Step<string> {
  getStepType(): string {
    return "AddItem";
  }

  getStepOptions(): StepOptions {
    return { executeLockAttributes: [cartLock.lock(), cartTotal.lock()] };
  }

  execute(context: Context, sku: string): StepDecision {
    cartTotal.set(context, cartTotal.get(context) + 1);
    cartItems.set(context, sku, 1);
    events.publish(context, sku, "added");
    return gracefulComplete();
  }
}

export class CartFlow implements Flow<string> {
  private readonly addItem = new AddItem();

  getFlowType(): string {
    return "CartFlow";
  }

  getSteps() {
    return StepList.startStep(this.addItem);
  }

  getPersistenceSchema(): PersistenceSchema {
    return { attributes: [cartLock, cartTotal, cartItems], channels: [replies, events] };
  }

  @rpc({ inputCodec: stringCodec, lockAttributes: [cartLock.lock()] })
  reply(context: Context, text: string): void {
    replies.publish(context, text);
  }
}

export const cartFlow = new CartFlow();
