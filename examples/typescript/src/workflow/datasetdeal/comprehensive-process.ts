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

import { DatasetDealAction } from "./actions.js";
import type { DealProcess } from "./models.js";

export function comprehensiveDealProcess(processId: string): DealProcess {
  return {
    processId,
    initialState: "buyer-negotiation",
    initialStateData: {
      acceptedProposedPrice: "false",
      proceedToFullDataset: "false",
      proposedFullPrice: "",
      proposedSamplePrice: "",
      proposedSampleRefundPrice: "",
    },
    states: [
      {
        name: "buyer-negotiation",
        preActions: [],
        postActions: [],
        postCondition: {
          waitFor: { name: "buyer-proposal" },
          decision: { key: "", cases: [], elseState: "seller-counteroffer" },
        },
      },
      {
        name: "seller-counteroffer",
        preCondition: { name: "seller-price-response" },
        preActions: [],
        postActions: [],
        postCondition: {
          decision: {
            key: "acceptedProposedPrice",
            cases: [{ equals: "true", goToState: "process-sample-order" }],
            elseState: "buyer-negotiation",
          },
        },
      },
      {
        name: "process-sample-order",
        preActions: [DatasetDealAction.TRANSFER_MONEY_FROM_BUYER_TO_SELLER],
        postActions: [DatasetDealAction.TRANSPORT_SAMPLE_DATASET_TO_BUYER],
        postCondition: {
          decision: { key: "", cases: [], elseState: "wait-sample-feedback" },
        },
      },
      {
        name: "wait-sample-feedback",
        preCondition: { name: "sample-feedback" },
        preActions: [],
        postActions: [],
        postCondition: {
          decision: {
            key: "proceedToFullDataset",
            cases: [{ equals: "true", goToState: "process-full-order" }],
            elseState: "process-refund",
          },
        },
      },
      {
        name: "process-full-order",
        preActions: [DatasetDealAction.TRANSFER_MONEY_FROM_BUYER_TO_SELLER],
        postActions: [DatasetDealAction.TRANSPORT_FULL_DATASET_TO_BUYER],
      },
      {
        name: "process-refund",
        preActions: [DatasetDealAction.TRANSFER_MONEY_FROM_SELLER_TO_BUYER],
        postActions: [],
      },
    ],
  };
}
