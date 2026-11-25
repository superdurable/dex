// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Attribute,
  Channel,
  StepList,
  booleanCodec,
  doubleCodec,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

const cartLock = new Attribute("CartLock", booleanCodec);
// Each option belongs to the other resource type, so neither declares a resource.
const cartTotal = new Attribute("CartTotal", doubleCodec).publishesRequireAttributeLock(cartLock);
const replies = new Channel("Replies", stringCodec).writesRequireLock();

// dex:group group-id:cart group-label:"Cart"
// dex:explanation text:"Write the total and publish a reply."
class AddItem implements Step<string> {
  getStepType(): string {
    return "AddItem";
  }

  execute(context: Context, sku: string): StepDecision {
    cartTotal.set(context, 1);
    replies.publish(context, sku);
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
    return { attributes: [cartLock, cartTotal], channels: [replies] };
  }
}

export const cartFlow = new CartFlow();
