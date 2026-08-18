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

import { jsonCodec } from "@superdurable/dex";

export interface OrderRequest {
  readonly orderId: string;
  readonly email: string;
  readonly customerId: string;
  readonly amount: number;
  readonly failShip: boolean;
}

export const orderRequestCodec = jsonCodec<OrderRequest>({
  typeName: "OrderRequest",
  decode: (value: unknown): OrderRequest => {
    const record = value as OrderRequest;
    return {
      orderId: String(record.orderId),
      email: String(record.email),
      customerId: String(record.customerId),
      amount: Number(record.amount),
      failShip: Boolean(record.failShip),
    };
  },
});
