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

import type { StateData } from "./models.js";

export const DatasetDealAction = Object.freeze({
  TRANSFER_MONEY_FROM_BUYER_TO_SELLER: "transferMoneyFromBuyerToSeller",
  TRANSFER_MONEY_FROM_SELLER_TO_BUYER: "transferMoneyFromSellerToBuyer",
  TRANSPORT_FULL_DATASET_TO_BUYER: "transportFullDatasetToBuyer",
  TRANSPORT_SAMPLE_DATASET_TO_BUYER: "transportSampleDatasetToBuyer",
} as const);

export interface ActionInput {
  readonly flowId: string;
  readonly processId: string;
  readonly buyerId: string;
  readonly targetState: string;
  readonly stateData: StateData;
}

type ActionHandler = (input: ActionInput) => StateData | Promise<StateData>;

export class DatasetDealActions {
  private readonly handlers: ReadonlyMap<string, ActionHandler>;

  public constructor() {
    this.handlers = new Map<string, ActionHandler>([
      [
        DatasetDealAction.TRANSFER_MONEY_FROM_BUYER_TO_SELLER,
        transferMoneyFromBuyerToSeller,
      ],
      [
        DatasetDealAction.TRANSFER_MONEY_FROM_SELLER_TO_BUYER,
        transferMoneyFromSellerToBuyer,
      ],
      [DatasetDealAction.TRANSPORT_FULL_DATASET_TO_BUYER, transportFullDatasetToBuyer],
      [DatasetDealAction.TRANSPORT_SAMPLE_DATASET_TO_BUYER, transportSampleDatasetToBuyer],
    ]);
  }

  public availableNames(): readonly string[] {
    return [...this.handlers.keys()].sort();
  }

  public async execute(name: string, input: ActionInput): Promise<StateData> {
    const handler = this.handlers.get(name);
    if (handler === undefined) {
      throw new TypeError(`dataset deal action ${name} is not registered`);
    }
    return {
      ...(await handler(input)),
      lastAction: name,
      lastActionStatus: "completed",
    };
  }
}

function transferMoneyFromBuyerToSeller(input: ActionInput): StateData {
  console.info("dataset deal transferred money from buyer to seller", {
    flowId: input.flowId,
    buyerId: input.buyerId,
    targetState: input.targetState,
    samplePrice: input.stateData.proposedSamplePrice,
    fullPrice: input.stateData.proposedFullPrice,
  });
  return {};
}

function transferMoneyFromSellerToBuyer(input: ActionInput): StateData {
  console.info("dataset deal transferred refund from seller to buyer", {
    flowId: input.flowId,
    buyerId: input.buyerId,
    refundPrice: input.stateData.proposedSampleRefundPrice,
  });
  return {};
}

function transportFullDatasetToBuyer(input: ActionInput): StateData {
  console.info("dataset deal transported full dataset to buyer", {
    flowId: input.flowId,
    buyerId: input.buyerId,
  });
  return { deliveredDataset: "full" };
}

function transportSampleDatasetToBuyer(input: ActionInput): StateData {
  console.info("dataset deal transported sample dataset to buyer", {
    flowId: input.flowId,
    buyerId: input.buyerId,
  });
  return { deliveredDataset: "sample" };
}
